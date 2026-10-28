package planexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func rawAdapterFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adapter with spaces")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRawAdapterPreservesOpaqueInputBytes(t *testing.T) {
	// Opaque transport data, not a fabricated native producer or semantic unit.
	payload := append(bytes.Repeat([]byte("a\x00é\r\n"), 20000), []byte("no-final-newline")...)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	actual, err := invokeRawAdapter(ctx, rawAdapterFixture(t, "exec /bin/cat"), payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, payload) {
		t.Fatalf("raw transport changed bytes: got %d, want %d", len(actual), len(payload))
	}
}

func TestLegacyAdapterInputRemainsExactPacketJSON(t *testing.T) {
	packet := onePacket().Packets[0]
	expected, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := invokeAdapter(context.Background(), rawAdapterFixture(t, "exec /bin/cat"), packet)
	if err != nil || !bytes.Equal(actual, expected) {
		t.Fatalf("legacy input serialization changed: %v", err)
	}
}

func TestRawAdapterInputBoundRefusesBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "launched")
	adapter := rawAdapterFixture(t, "/bin/cat >/dev/null\nprintf ok\nprintf reached > "+shellQuote(marker))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := invokeRawAdapter(ctx, adapter, bytes.Repeat([]byte("x"), maxRunBytes))
	if err != nil || string(output) != "ok" {
		t.Fatalf("valid exact input bound did not run: %v", err)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal("positive launch marker missing")
	}
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	output, err = invokeRawAdapter(ctx, adapter, bytes.Repeat([]byte("x"), maxRunBytes+1))
	if err == nil || !strings.Contains(err.Error(), "packet input exceeds byte budget") || output != nil {
		t.Fatalf("oversized input accepted: %v", err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("oversized input spawned adapter")
	}
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// These fixtures record only their own PID and the invocation-owned process
// group. No global process scan or caller-selected group is used for cleanup.
type ownedAdapterChild struct{ pid, group int }

func readOwnedAdapterChild(t *testing.T, marker string) ownedAdapterChild {
	t.Helper()
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	var child ownedAdapterChild
	if _, err = fmt.Sscan(string(raw), &child.pid, &child.group); err != nil || child.pid <= 1 || child.group <= 1 {
		t.Fatal("invalid owned child marker")
	}
	t.Cleanup(func() {
		group, err := syscall.Getpgid(child.pid)
		if err == syscall.ESRCH {
			return
		}
		if err != nil {
			t.Error(err)
			return
		}
		if group != child.group {
			t.Error("refusing unrelated cleanup group")
			return
		}
		if err = syscall.Kill(-child.group, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			t.Error(err)
			return
		}
		if !waitAdapterChildStopped(t, child.pid, time.Second) {
			t.Error("test-owned child cleanup failed")
		}
	})
	return child
}

func waitAdapterChildStopped(t *testing.T, pid int, bound time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		} else if err != nil {
			t.Fatal(err)
		}
		state, err := exec.Command("/bin/ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			if syscall.Kill(pid, 0) == syscall.ESRCH {
				return true
			}
			t.Fatal(err)
		}
		// An init-owned zombie cannot execute; the operating system owns reaping.
		if strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func backgroundAdapterBody(marker, ending string) string {
	return "/bin/cat >/dev/null\n/bin/sleep 60 >/dev/null 2>&1 &\nprintf '%s %s\\n' \"$!\" \"$$\" > " + shellQuote(marker) + "\n" + ending
}

func TestAdapterCleansDetachedStdioChildAfterNormalReturn(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "raw"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "owned.pid")
			adapter := rawAdapterFixture(t, backgroundAdapterBody(marker, "printf '{\"findings\":[]}'"))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var output []byte
			var err error
			if legacy {
				output, err = invokeAdapter(ctx, adapter, onePacket().Packets[0])
			} else {
				output, err = invokeRawAdapter(ctx, adapter, []byte("opaque transport control"))
			}
			if err != nil || string(output) != "{\"findings\":[]}" {
				t.Fatalf("valid parent did not complete: %v", err)
			}
			child := readOwnedAdapterChild(t, marker)
			if !waitAdapterChildStopped(t, child.pid, time.Second) {
				t.Fatal("successful adapter left its same-group child running")
			}
		})
	}
}

func TestRawAdapterFailurePathsStopOwnedBackgroundChildren(t *testing.T) {
	cases := []struct {
		name, ending, want string
		timeout            time.Duration
	}{
		{"nonzero", "printf partial; exit 7", "run adapter:", 3 * time.Second},
		{"stdout", "/usr/bin/head -c 1048577 /dev/zero", "byte budget", 3 * time.Second},
		{"stderr", "/usr/bin/head -c 65537 /dev/zero >&2", "byte budget", 3 * time.Second},
		{"timeout", "wait", "run adapter:", time.Second},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "owned.pid")
			adapter := rawAdapterFixture(t, backgroundAdapterBody(marker, test.ending))
			ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
			defer cancel()
			started := time.Now()
			output, err := invokeRawAdapter(ctx, adapter, []byte("opaque input"))
			child := readOwnedAdapterChild(t, marker)
			if err == nil || !strings.Contains(err.Error(), test.want) || output != nil {
				t.Fatalf("adapter failure hidden or invented output: err=%v stdout_bytes=%d", err, len(output))
			}
			if time.Since(started) > test.timeout+2*time.Second {
				t.Fatal("adapter failure exceeded its wall bound")
			}
			if !waitAdapterChildStopped(t, child.pid, time.Second) {
				t.Fatal("failed adapter left its same-group child running")
			}
		})
	}
}

func TestRawAdapterUnreadStdinIsCancellable(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started.pid")
	adapter := rawAdapterFixture(t, "printf '%s %s\\n' \"$$\" \"$$\" > "+shellQuote(marker)+"\nexec /bin/sleep 60")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := invokeRawAdapter(ctx, adapter, bytes.Repeat([]byte("x"), 512*1024)); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unread-stdin child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	child := readOwnedAdapterChild(t, marker)
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled unread-stdin adapter succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unread stdin escaped cancellation")
	}
	if time.Since(started) > 2*time.Second || !waitAdapterChildStopped(t, child.pid, time.Second) {
		t.Fatal("unread-stdin child survived cancellation")
	}
}

func TestRawAdapterEarlyInputCloseDoesNotProveConsumption(t *testing.T) {
	// A successful process exit is deliberately NOT a source-delivery receipt.
	// The controller must still require its independently bound delivery/usage.
	output, err := invokeRawAdapter(context.Background(), rawAdapterFixture(t, "printf unread"), bytes.Repeat([]byte("x"), 512*1024))
	if err != nil || string(output) != "unread" {
		t.Fatalf("early-close characterization changed: %v", err)
	}
}

func TestRawAdapterCancelledBeforeStartNeverLaunches(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "launched")
	adapter := rawAdapterFixture(t, "printf reached > "+shellQuote(marker))
	for range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		output, err := invokeRawAdapter(ctx, adapter, []byte("input"))
		if err == nil || output != nil {
			t.Fatal("cancelled adapter invocation succeeded")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("cancelled invocation started a child")
	}
}

func TestRawAdapterSpawnFailurePreservesPrimaryError(t *testing.T) {
	output, err := invokeRawAdapter(context.Background(), filepath.Join(t.TempDir(), "absent"), []byte("input"))
	if err == nil || !strings.Contains(err.Error(), "run adapter:") || strings.Contains(err.Error(), "cleanup") || output != nil {
		t.Fatalf("spawn failure lost primary cause: %v", err)
	}
}

func TestAdapterGroupCleanupRefusesUnownedDescriptors(t *testing.T) {
	for _, command := range []*exec.Cmd{nil, {}, {Process: &os.Process{Pid: 0}}, {Process: &os.Process{Pid: 1}}, {Process: &os.Process{Pid: -1}}} {
		if err := terminateAdapterGroup(command); err == nil {
			t.Fatal("invalid group accepted")
		}
	}
	child := exec.Command("/bin/sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	if err := terminateAdapterGroup(child); err == nil || !strings.Contains(err.Error(), "invocation-owned") {
		t.Fatal("ordinary shared process group accepted")
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("unowned group check signaled the child")
	}
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: child.Process.Pid}
	if err := terminateAdapterGroup(child); err == nil || !strings.Contains(err.Error(), "invocation-owned") {
		t.Fatal("caller-selected group accepted")
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatal("caller-selected group check signaled the child")
	}
}

func TestLimitedOutputCopyCannotBypassItsWriteBound(t *testing.T) {
	for _, size := range []int{8, 9} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			cancelled := false
			writer := &limitedOutput{limit: 8, cancel: func() { cancelled = true }}
			// os/exec copies pipe data through io.Copy. Hide the source's WriterTo so
			// the destination's optional interfaces cannot bypass the output boundary.
			reader := struct{ io.Reader }{strings.NewReader(strings.Repeat("x", size))}
			copied, err := io.Copy(writer, reader)
			if size == 8 {
				if err != nil || copied != 8 || len(writer.Bytes()) != 8 || cancelled {
					t.Fatal("valid exact output bound refused")
				}
			} else if err == nil || !cancelled || !writer.overflow || len(writer.Bytes()) > 8 {
				t.Fatalf("copy bypassed output bound: copied=%d captured=%d cancelled=%v err=%v", copied, len(writer.Bytes()), cancelled, err)
			}
		})
	}
}
