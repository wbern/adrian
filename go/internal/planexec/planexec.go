// Package planexec transports opaque JSON to a caller-selected executable.
// It neither selects a model nor interprets a review verdict. The consumer owns
// assignment authentication, semantic fan-in, current-head checks and publication.
package planexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/wbern/adrian/go/internal/adr"
	"github.com/wbern/adrian/go/internal/reviewpacket"
)

const RunSchemaVersion = "review-packet-run-v1"
const Usage = "adrian execute-plan --plan <plan.json> --adapter <executable> --expected-plan-sha256 <trusted hash> --record <run.json> [--timeout 60s]"
const maxResultBytes = 1024 * 1024
const maxRunBytes = 8 * 1024 * 1024

type Invoker func(context.Context, reviewpacket.Packet) ([]byte, error)
type Run struct {
	SchemaVersion     string    `json:"schemaVersion"`
	PlanSHA256        string    `json:"planSha256"`
	Receipts          []Receipt `json:"receipts"`
	Incomplete        bool      `json:"incomplete,omitempty"`
	FailedPacketIndex int       `json:"failedPacketIndex,omitempty"`
	Failure           string    `json:"failure,omitempty"`
}
type Receipt struct {
	PacketIndex  int             `json:"packetIndex"`
	PacketSHA256 string          `json:"packetSha256"`
	Result       json.RawMessage `json:"result"`
}

// The public command requires out-of-band producer identity and durable attempt
// storage. RunCLI also supports legacy in-process callers whose collector owns
// persistence; no transport JSON is a semantic approval receipt by itself.
func RunCommand(args []string, _ string, out io.Writer) error {
	opts, err := parseCLIArgs(args)
	if err != nil {
		return err
	}
	if opts.expected == "" || opts.record == "" {
		return fmt.Errorf("trusted expected hash and durable record required; %s", Usage)
	}
	return RunCLI(context.Background(), args, out)
}

type options struct {
	plan, adapter, expected, record string
	timeout                         time.Duration
}

func parseCLIArgs(args []string) (options, error) {
	o := options{timeout: 60 * time.Second}
	seen := map[string]bool{}
	for i := 0; i < len(args); i += 2 {
		flag := args[i]
		if seen[flag] || i+1 >= len(args) || args[i+1] == "" || strings.HasPrefix(args[i+1], "--") {
			return o, fmt.Errorf("duplicate/missing value for %s", flag)
		}
		seen[flag] = true
		value := args[i+1]
		switch flag {
		case "--plan":
			o.plan = value
		case "--adapter":
			o.adapter = value
		case "--expected-plan-sha256":
			o.expected = value
		case "--record":
			o.record = value
		case "--timeout":
			duration, err := time.ParseDuration(value)
			if err != nil || duration <= 0 || duration > 30*time.Minute {
				return o, fmt.Errorf("invalid timeout")
			}
			o.timeout = duration
		default:
			return o, fmt.Errorf("unknown argument: %s", flag)
		}
	}
	if o.plan == "" || o.adapter == "" {
		return o, fmt.Errorf("usage: %s", Usage)
	}
	return o, nil
}
func RunCLI(ctx context.Context, args []string, out io.Writer) error {
	o, err := parseCLIArgs(args)
	if err != nil {
		return err
	}
	file, err := os.Open(o.plan)
	if err != nil {
		return err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxRunBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > maxRunBytes {
		return fmt.Errorf("plan exceeds byte budget")
	}
	var plan reviewpacket.Plan
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return fmt.Errorf("decode plan: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("plan must contain one JSON value")
	}
	if err := reviewpacket.ValidatePlan(plan); err != nil {
		return err
	}
	if o.expected != "" && o.expected != plan.PlanSHA256 {
		return fmt.Errorf("trusted plan hash mismatch")
	}
	var record func(Run) error
	if o.record != "" {
		reserved, err := os.OpenFile(o.record, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("reserve immutable attempt path: %w", err)
		}
		if err := reserved.Close(); err != nil {
			return err
		}
		record = func(run Run) error {
			payload, err := encodeRun(run)
			if err != nil {
				return err
			}
			return adr.WriteFileAtomic(o.record, append(payload, '\n'), 0600)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	run, runErr := execute(ctx, plan, func(ctx context.Context, packet reviewpacket.Packet) ([]byte, error) {
		return invokeAdapter(ctx, o.adapter, packet)
	}, record)
	payload, err := encodeRun(run)
	if err != nil {
		return err
	}
	if _, err := out.Write(append(payload, '\n')); err != nil {
		return err
	}
	return runErr
}

// Execute preserves the complete partial attempt on failure. Its caller must
// pin the expected producer hash independently and persist returned evidence.
func Execute(ctx context.Context, plan reviewpacket.Plan, invoke Invoker) (Run, error) {
	return execute(ctx, plan, invoke, nil)
}
func execute(ctx context.Context, plan reviewpacket.Plan, invoke Invoker, record func(Run) error) (run Run, err error) {
	run = Run{SchemaVersion: RunSchemaVersion, PlanSHA256: plan.PlanSHA256, Receipts: []Receipt{}, Incomplete: true}
	defer func() {
		if err != nil {
			run.Incomplete = true
			run.Failure = boundedFailure(err)
		}
		if record != nil {
			if saveErr := record(run); saveErr != nil {
				err = fmt.Errorf("persist attempt: %w", saveErr)
				run.Incomplete = true
				run.Failure = boundedFailure(err)
			}
		}
	}()
	if err = reviewpacket.ValidatePlan(plan); err != nil {
		return run, fmt.Errorf("invalid plan: %w", err)
	}
	if plan.Limits.PacketLimitExceeded || (plan.Limits.MaxPackets > 0 && len(plan.Packets) > plan.Limits.MaxPackets) {
		return run, fmt.Errorf("plan exceeds packet limit")
	}
	if invoke == nil {
		return run, fmt.Errorf("adapter required")
	}
	if record != nil {
		if err = record(run); err != nil {
			return run, err
		}
	}
	total := 0
	for index, packet := range plan.Packets {
		run.FailedPacketIndex = index + 1
		if err = ctx.Err(); err != nil {
			return run, err
		}
		var result []byte
		result, err = invoke(ctx, packet)
		if err != nil {
			return run, fmt.Errorf("packet %d: %w", index+1, err)
		}
		if len(result) > maxResultBytes || total+len(result) > maxRunBytes {
			return run, fmt.Errorf("adapter result exceeds byte budget")
		}
		if !json.Valid(result) {
			return run, fmt.Errorf("packet %d: adapter returned invalid JSON", index+1)
		}
		total += len(result)
		receipt := Receipt{PacketIndex: index + 1, PacketSHA256: reviewpacket.PacketSHA256(packet), Result: append(json.RawMessage(nil), result...)}
		candidate := run
		candidate.Receipts = append(append([]Receipt{}, run.Receipts...), receipt)
		encoded, encodeErr := encodeRun(candidate)
		if encodeErr != nil || len(encoded) > maxRunBytes-4096 {
			return run, fmt.Errorf("encoded attempt exceeds byte budget")
		}
		run.Receipts = candidate.Receipts
		if record != nil {
			if err = record(run); err != nil {
				return run, err
			}
		}
	}
	run.Incomplete = false
	run.FailedPacketIndex = 0
	return run, nil
}

type limitedOutput struct {
	// A named buffer prevents io.Copy from using a promoted ReadFrom method
	// that would bypass Write's output bound.
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (b *limitedOutput) Write(raw []byte) (int, error) {
	if len(raw) > b.limit-b.buffer.Len() {
		b.overflow = true
		b.cancel()
		return 0, fmt.Errorf("adapter output exceeds byte budget")
	}
	return b.buffer.Write(raw)
}
func (b *limitedOutput) Bytes() []byte { return b.buffer.Bytes() }

func invokeAdapter(ctx context.Context, path string, packet reviewpacket.Packet) ([]byte, error) {
	payload, err := json.Marshal(packet)
	if err != nil {
		return nil, err
	}
	return invokeRawAdapter(ctx, path, payload)
}

// invokeRawAdapter transports opaque bytes. Its caller owns content authority,
// attempt persistence and execution isolation; this is not a public unit API.
func invokeRawAdapter(ctx context.Context, path string, payload []byte) (output []byte, err error) {
	if len(payload) > maxRunBytes {
		return nil, fmt.Errorf("packet input exceeds byte budget")
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(child, path)
	command.Stdin = bytes.NewReader(payload)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return terminateAdapterGroup(command) }
	command.WaitDelay = time.Second
	stdout := &limitedOutput{limit: maxResultBytes, cancel: cancel}
	stderr := &limitedOutput{limit: 64 * 1024, cancel: cancel}
	command.Stdout = stdout
	command.Stderr = stderr
	if err = command.Start(); err != nil {
		return nil, fmt.Errorf("run adapter: %w", err)
	}
	defer func() {
		// Setpgid allocated this command's group. Read Process only after
		// successful Start; Cancel also reads that checked, published Process.
		cleanupErr := terminateAdapterGroup(command)
		if cleanupErr == nil || errors.Is(cleanupErr, syscall.ESRCH) {
			return
		}
		output = nil
		if err != nil {
			err = fmt.Errorf("%w; cleanup adapter process group: %w", err, cleanupErr)
		} else {
			err = fmt.Errorf("cleanup adapter process group: %w", cleanupErr)
		}
	}()
	err = command.Wait()
	if stdout.overflow || stderr.overflow {
		return nil, fmt.Errorf("adapter output exceeds byte budget")
	}
	if err != nil {
		return nil, fmt.Errorf("run adapter: %w", err)
	}
	return stdout.Bytes(), nil
}

// terminateAdapterGroup accepts only the group allocated by this invocation.
// It does not discover processes or accept a group identifier from adapter data.
func terminateAdapterGroup(command *exec.Cmd) error {
	if command == nil || command.Process == nil || command.Process.Pid <= 1 ||
		command.SysProcAttr == nil || !command.SysProcAttr.Setpgid || command.SysProcAttr.Pgid != 0 {
		return fmt.Errorf("adapter has no invocation-owned process group")
	}
	return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}

func boundedFailure(err error) string {
	message := err.Error()
	if len(message) > 2048 {
		message = message[:2048]
	}
	return message
}
func encodeRun(run Run) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(run); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(out.Bytes(), []byte{'\n'})
	if len(raw) > maxRunBytes {
		return nil, fmt.Errorf("encoded attempt exceeds byte budget")
	}
	return raw, nil
}
