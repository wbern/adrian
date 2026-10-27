//go:build unix

package corpus

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRequireRejectsNamedPipe(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "0001-pipe.md"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Require(dir); err == nil {
		t.Fatal("named pipe accepted as corpus record")
	}
	if err := os.Remove(filepath.Join(dir, "0001-pipe.md")); err != nil {
		t.Fatal(err)
	}
}
