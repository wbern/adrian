// Package planexec executes a review packet plan in deterministic order.
// The supplied invoker receives exactly one packet at a time and cannot alter
// the plan inventory.
package planexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/wbern/adr-lint/go/internal/reviewpacket"
)

const RunSchemaVersion = "review-packet-run-v1"

// Invoker performs one adapter call for one planner-selected packet.
type Invoker func(context.Context, reviewpacket.Packet) ([]byte, error)

// Run is the deterministic execution record for a plan.
type Run struct {
	SchemaVersion string    `json:"schemaVersion"`
	PlanSHA256    string    `json:"planSha256"`
	Receipts      []Receipt `json:"receipts"`
}

// Receipt records one completed adapter call without interpreting its result.
type Receipt struct {
	PacketIndex  int             `json:"packetIndex"`
	PacketSHA256 string          `json:"packetSha256"`
	Result       json.RawMessage `json:"result"`
}

// RunCommand is the adr-lint subcommand entrypoint. It deliberately accepts only a
// plan and an adapter executable: model selection and packet scheduling are
// outside this mechanical executor.
func RunCommand(args []string, _ string, out io.Writer) error {
	return RunCLI(context.Background(), args, out)
}

// RunCLI loads an immutable plan and sends its packets, one at a time, to an
// executable adapter. Each packet is JSON on standard input; the adapter must
// return one JSON value on standard output.
func RunCLI(ctx context.Context, args []string, out io.Writer) error {
	planPath, adapterPath, err := parseCLIArgs(args)
	if err != nil {
		return err
	}
	contents, err := os.ReadFile(planPath)
	if err != nil {
		return fmt.Errorf("read plan: %w", err)
	}
	var plan reviewpacket.Plan
	if err := json.Unmarshal(contents, &plan); err != nil {
		return fmt.Errorf("decode plan: %w", err)
	}
	run, err := Execute(ctx, plan, func(ctx context.Context, packet reviewpacket.Packet) ([]byte, error) {
		payload, err := json.Marshal(packet)
		if err != nil {
			return nil, fmt.Errorf("encode packet: %w", err)
		}
		command := exec.CommandContext(ctx, adapterPath)
		command.Stdin = bytes.NewReader(payload)
		result, err := command.Output()
		if err != nil {
			return nil, fmt.Errorf("run adapter: %w", err)
		}
		return result, nil
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(run)
}

func parseCLIArgs(args []string) (planPath, adapterPath string, err error) {
	for index := 0; index < len(args); index += 2 {
		if index+1 >= len(args) {
			return "", "", fmt.Errorf("%s requires a value", args[index])
		}
		switch args[index] {
		case "--plan":
			if planPath != "" {
				return "", "", fmt.Errorf("--plan may be specified once")
			}
			planPath = args[index+1]
		case "--adapter":
			if adapterPath != "" {
				return "", "", fmt.Errorf("--adapter may be specified once")
			}
			adapterPath = args[index+1]
		default:
			return "", "", fmt.Errorf("unknown argument: %s", args[index])
		}
	}
	if planPath == "" || adapterPath == "" {
		return "", "", fmt.Errorf("usage: adr-lint execute-plan --plan <plan.json> --adapter <executable>")
	}
	return planPath, adapterPath, nil
}

// Execute invokes every packet in order. It stops at the first adapter error.
func Execute(ctx context.Context, plan reviewpacket.Plan, invoke Invoker) (Run, error) {
	run := Run{
		SchemaVersion: RunSchemaVersion,
		PlanSHA256:    plan.PlanSHA256,
		Receipts:      []Receipt{},
	}
	if err := reviewpacket.ValidatePlan(plan); err != nil {
		return run, fmt.Errorf("invalid plan: %w", err)
	}
	if plan.Limits.PacketLimitExceeded {
		return run, fmt.Errorf("plan exceeds packet limit: %d packets, limit %d", len(plan.Packets), plan.Limits.MaxPackets)
	}
	for i, packet := range plan.Packets {
		result, err := invoke(ctx, packet)
		if err != nil {
			return run, fmt.Errorf("packet %d: %w", i+1, err)
		}
		if !json.Valid(result) {
			return run, fmt.Errorf("packet %d: adapter returned invalid JSON", i+1)
		}
		run.Receipts = append(run.Receipts, Receipt{
			PacketIndex:  i + 1,
			PacketSHA256: hashPacket(packet),
			Result:       append(json.RawMessage(nil), result...),
		})
	}
	return run, nil
}

func hashPacket(packet reviewpacket.Packet) string {
	payload, err := json.Marshal(packet)
	if err != nil {
		panic("planexec: marshal packet hash: " + err.Error())
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
