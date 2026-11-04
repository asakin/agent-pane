//go:build linux

package cmd

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

func TestHardenHostMakesProcessNotDumpable(t *testing.T) {
	if os.Getenv("HARDEN_CHILD") == "1" {
		if err := hardenHost(); err != nil {
			t.Fatal(err)
		}
		dumpable, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if dumpable != 0 {
			t.Fatalf("dumpable = %d", dumpable)
		}
		return
	}
	// Run in a child so the test binary itself stays dumpable.
	child := exec.Command(os.Args[0], "-test.run=^TestHardenHostMakesProcessNotDumpable$")
	child.Env = append(os.Environ(), "HARDEN_CHILD=1")
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
