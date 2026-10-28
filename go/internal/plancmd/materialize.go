package plancmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/wbern/adrian/go/internal/sharedpacket"
)

const MaterializeUsage = "adrian materialize --plan <code-plan.json> --expected-plan-fingerprint <trusted fingerprint> --expected-shared-fingerprint <trusted fingerprint> --atom <atom id>"

// The producer admits this many encoded bytes before appending its final newline.
const maxMaterializePlanBytes = defaultMaxCodePlanBytes + 1

var materializeFingerprint = regexp.MustCompile(`^[0-9a-f]{64}$`)

// RunMaterializeCLI exposes existing fragment integrity checks, not assessment,
// execution or completeness of a rule review. Both pins belong to the caller.
func RunMaterializeCLI(args []string, out io.Writer) error {
	values := map[string]string{}
	for i := 0; i < len(args); i += 2 {
		flag := args[i]
		if i+1 >= len(args) || args[i+1] == "" || values[flag] != "" {
			return fmt.Errorf("duplicate/missing materialize argument; %s", MaterializeUsage)
		}
		switch flag {
		case "--plan", "--expected-plan-fingerprint", "--expected-shared-fingerprint", "--atom":
			values[flag] = args[i+1]
		default:
			return fmt.Errorf("unknown materialize argument %s", flag)
		}
	}
	if values["--plan"] == "" || values["--plan"] == "-" {
		return fmt.Errorf("materialize requires a regular plan file; %s", MaterializeUsage)
	}
	for _, flag := range []string{"--expected-plan-fingerprint", "--expected-shared-fingerprint", "--atom"} {
		if !materializeFingerprint.MatchString(values[flag]) {
			return fmt.Errorf("%s requires 64 lowercase hexadecimal characters", flag)
		}
	}
	info, err := os.Lstat(values["--plan"])
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxMaterializePlanBytes {
		return fmt.Errorf("materialize plan must be a bounded regular file")
	}
	file, err := os.Open(values["--plan"])
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxMaterializePlanBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > maxMaterializePlanBytes || !utf8.Valid(raw) {
		return fmt.Errorf("materialize plan exceeds byte budget or is not UTF-8")
	}
	if err := materializeJSON(raw); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if err := materializeObjectFields(fields, reflect.TypeFor[CodePlan](), map[reflect.Type]map[string]reflect.Type{}); err != nil {
		return err
	}
	for _, legacy := range []string{"packet_plan", "delivery"} {
		if _, present := fields[legacy]; present {
			return fmt.Errorf("legacy delivery is not a shared code plan")
		}
	}
	var plan CodePlan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return fmt.Errorf("invalid code plan: %w", err)
	}
	if plan.SharedDelivery == nil {
		return fmt.Errorf("materialize requires a shared code plan")
	}
	switch plan.Schema {
	case "adrian-code-plan-v2":
		if _, present := fields["repository_kind"]; present {
			return fmt.Errorf("repository_kind requires code-plan V3")
		}
	case CodeIdentitySchema:
		_, present := fields["repository_kind"]
		if !present || (plan.RepositoryKind != "github" && plan.RepositoryKind != "local") || bindingRepositoryKind(plan.Repository) != plan.RepositoryKind {
			return fmt.Errorf("V3 repository_kind must match the declared repository identity")
		}
	default:
		return fmt.Errorf("materialize requires a V2 or V3 shared code plan")
	}
	if plan.Fingerprint != values["--expected-plan-fingerprint"] {
		return fmt.Errorf("code plan fingerprint differs from trusted expectation")
	}
	stored := plan.Fingerprint
	plan.Fingerprint = ""
	canonical, err := json.Marshal(plan)
	plan.Fingerprint = stored
	if err != nil || digest(canonical) != stored {
		return fmt.Errorf("code plan fingerprint mismatch")
	}
	shared := plan.SharedDelivery
	if shared.Schema != sharedpacket.Schema || shared.Fingerprint != values["--expected-shared-fingerprint"] ||
		!materializeFingerprint.MatchString(plan.Coverage.Fingerprint) || shared.CoverageFingerprint != plan.Coverage.Fingerprint {
		return fmt.Errorf("shared delivery identity or coverage link mismatch")
	}
	inputs, err := materializeInputs(plan)
	if err != nil {
		return err
	}
	// Reuse the native API for each artifact's identity and first fragment. Do
	// not rebuild its chunks, decode stored base64 or consult repository files.
	for _, artifact := range shared.Artifacts {
		if _, err := sharedpacket.Materialize(*shared, artifact.Atoms[0], inputs, values["--expected-shared-fingerprint"]); err != nil {
			return err
		}
	}
	result, err := sharedpacket.Materialize(*shared, values["--atom"], inputs, values["--expected-shared-fingerprint"])
	if err != nil {
		return err
	}
	_, err = out.Write(result)
	return err
}

// Scan tokens before typed decoding because encoding/json accepts duplicate
// object keys. Keep recursive JSON metadata bounded independently of byte size.
func materializeJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := materializeJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("materialize requires one JSON document")
	}
	return nil
}

func materializeJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 256 {
		return fmt.Errorf("materialize JSON nesting exceeds capacity")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate/invalid JSON field %v", key)
			}
			seen[name] = true
			if err := materializeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := materializeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil || (delimiter == '{' && closing != json.Delim('}')) || (delimiter == '[' && closing != json.Delim(']')) {
		return fmt.Errorf("invalid JSON container")
	}
	return nil
}

// DisallowUnknownFields still permits case-folded aliases of struct fields.
// Derive exact spellings from the producer's JSON tags, including promoted
// embedded fields. Keys inside metadata maps remain data, not schema fields.
func materializeObjectFields(object map[string]json.RawMessage, typ reflect.Type, cache map[reflect.Type]map[string]reflect.Type) error {
	fields := materializeStructFields(typ, cache)
	for name, value := range object {
		field, known := fields[name]
		if !known {
			return fmt.Errorf("unknown or noncanonical JSON field %q", name)
		}
		if err := materializeTypedFields(value, field, cache); err != nil {
			return err
		}
	}
	return nil
}

func materializeStructFields(typ reflect.Type, cache map[reflect.Type]map[string]reflect.Type) map[string]reflect.Type {
	if fields, exists := cache[typ]; exists {
		return fields
	}
	fields := map[string]reflect.Type{}
	cache[typ] = fields
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || name == "-" {
			continue
		}
		embedded := field.Type
		for embedded.Kind() == reflect.Pointer {
			embedded = embedded.Elem()
		}
		if field.Anonymous && name == "" && embedded.Kind() == reflect.Struct {
			for name, nested := range materializeStructFields(embedded, cache) {
				fields[name] = nested
			}
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func materializeTypedFields(raw json.RawMessage, typ reflect.Type, cache map[reflect.Type]map[string]reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		return materializeObjectFields(object, typ, cache)
	case reflect.Slice, reflect.Array:
		if typ.Elem().Kind() == reflect.Uint8 {
			return nil // Encoded bytes/RawMessage have their own representation.
		}
		var elements []json.RawMessage
		if err := json.Unmarshal(raw, &elements); err != nil {
			return err
		}
		for _, value := range elements {
			if err := materializeTypedFields(value, typ.Elem(), cache); err != nil {
				return err
			}
		}
	case reflect.Map:
		if typ.Elem().Kind() == reflect.Interface {
			return nil
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		for _, value := range object {
			if err := materializeTypedFields(value, typ.Elem(), cache); err != nil {
				return err
			}
		}
	}
	return nil
}

func materializeInputs(plan CodePlan) ([]sharedpacket.Input, error) {
	shared := plan.SharedDelivery
	if shared.Limits.FragmentLimitExceeded || shared.Limits.MaxFragments <= 0 || shared.Limits.MaxFragmentInputTokens <= 0 || len(shared.Atoms) > shared.Limits.MaxFragments {
		return nil, fmt.Errorf("shared delivery exceeds fragment capacity")
	}
	inputs := make([]sharedpacket.Input, len(shared.Artifacts))
	refs := map[sharedpacket.Ref]bool{}
	artifacts := map[string]int{}
	for i, artifact := range shared.Artifacts {
		ref := artifact.CanonicalRef
		if !materializeFingerprint.MatchString(artifact.ID) || refs[ref] || ref.Index < 0 || artifact.Encoding != "utf-8" {
			return nil, fmt.Errorf("invalid/duplicate canonical artifact")
		}
		if _, duplicate := artifacts[artifact.ID]; duplicate {
			return nil, fmt.Errorf("duplicate canonical artifact identity")
		}
		var content string
		switch {
		case ref.Collection == "policies" && ref.Field == "content" && ref.Index < len(plan.Policies) && artifact.Kind == "policy":
			content = plan.Policies[ref.Index].Content
		case ref.Collection == "content" && ref.Field == "content" && ref.Index < len(plan.Content) && artifact.Kind == "source":
			content = plan.Content[ref.Index].Content
		case ref.Collection == "root" && ref.Field == "diff" && ref.Index == 0 && artifact.Kind == "global-diff":
			content = plan.Diff
		case ref.Collection == "change_diffs" && ref.Field == "diff" && ref.Index < len(plan.ChangeDiffs) && artifact.Kind == "change-diff":
			content = plan.ChangeDiffs[ref.Index].Diff
		default:
			return nil, fmt.Errorf("unsupported embedded canonical reference")
		}
		if artifact.ByteLength != len(content) || artifact.RepresentationSHA256 != digest([]byte(content)) || len(artifact.Atoms) == 0 {
			return nil, fmt.Errorf("canonical artifact bytes or membership changed")
		}
		refs[ref] = true
		artifacts[artifact.ID] = i
		inputs[i] = sharedpacket.Input{Kind: artifact.Kind, CanonicalRef: ref, Encoding: artifact.Encoding, Metadata: artifact.Metadata, Content: content}
	}
	atoms := map[string]sharedpacket.Atom{}
	for _, atom := range shared.Atoms {
		index, known := artifacts[atom.ArtifactID]
		if _, duplicate := atoms[atom.ID]; duplicate || !known || !materializeFingerprint.MatchString(atom.ID) {
			return nil, fmt.Errorf("unknown/duplicate fragment identity")
		}
		content := inputs[index].Content
		if atom.Offset < 0 || atom.Length < 0 || atom.Offset > len(content) || atom.Length > len(content)-atom.Offset ||
			atom.EstimatedInputTokens <= 0 || atom.EstimatedInputTokens > shared.Limits.MaxFragmentInputTokens {
			return nil, fmt.Errorf("fragment range or capacity invalid")
		}
		piece := content[atom.Offset : atom.Offset+atom.Length]
		if !utf8.ValidString(piece) || digest([]byte(piece)) != atom.SHA256 {
			return nil, fmt.Errorf("fragment bytes changed")
		}
		atoms[atom.ID] = atom
	}
	usedAtoms := map[string]bool{}
	for _, artifact := range shared.Artifacts {
		offset := 0
		for _, id := range artifact.Atoms {
			atom, known := atoms[id]
			if !known || usedAtoms[id] || atom.ArtifactID != artifact.ID || atom.Offset != offset {
				return nil, fmt.Errorf("fragment inventory is incomplete or ambiguous")
			}
			usedAtoms[id] = true
			offset += atom.Length
		}
		if offset != artifact.ByteLength {
			return nil, fmt.Errorf("fragment inventory omits canonical bytes")
		}
	}
	if len(usedAtoms) != len(atoms) {
		return nil, fmt.Errorf("orphan fragment")
	}
	return inputs, nil
}
