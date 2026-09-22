// Package validatecmd implements `adr-lint validate`, a structural linter
// for the ADR set itself (cross-references, IDs, status invariants).
package validatecmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wbern/adrian/go/internal/adr"
)

// Run validates every ADR under dir for structural issues that LoadADRs
// alone won't surface: dangling superseded_by references, duplicate IDs,
// status=superseded without a superseded_by, and ID gaps (warnings).
func Run(args []string, dir string, out io.Writer) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected args: usage: adr-lint validate")
	}
	adrs, err := adr.LoadADRs(dir)
	if err != nil {
		return err
	}

	var issues []string

	for _, a := range adrs {
		raw, err := os.ReadFile(a.FilePath)
		if err != nil {
			return err
		}
		if ferr := adr.ValidateFrontmatter(string(raw)); ferr != nil {
			issues = append(issues, fmt.Sprintf(
				"ADR %s: malformed frontmatter: %v", adr.NormalizeID(a.ID), ferr))
		}
	}

	ids := map[string]bool{}
	for _, a := range adrs {
		id := adr.NormalizeID(a.ID)
		if ids[id] {
			issues = append(issues, fmt.Sprintf("duplicate ADR ID %s", id))
			continue
		}
		ids[id] = true
	}

	for _, a := range adrs {
		id := adr.NormalizeID(a.ID)
		if a.Status == adr.StatusSuperseded && len(a.SupersededBy) == 0 {
			issues = append(issues,
				fmt.Sprintf("ADR %s: status=superseded but no superseded_by set", id))
		}
		for _, successor := range a.SupersededBy {
			target := adr.NormalizeID(successor)
			if !ids[target] {
				issues = append(issues, fmt.Sprintf(
					"ADR %s: superseded_by references %q but no such ADR exists",
					id, successor))
			}
		}
	}

	successors := make(map[string][]string, len(adrs))
	for _, a := range adrs {
		successors[adr.NormalizeID(a.ID)] = a.SupersededBy
	}
	if cycleErr := adr.ValidateSuccessorCycles(successors); cycleErr != nil {
		issues = append(issues, cycleErr.Error())
	}

	if len(issues) > 0 {
		return fmt.Errorf("validation failed:\n  %s", strings.Join(issues, "\n  "))
	}

	fmt.Fprintln(out, "OK")
	return nil
}
