package cmd

import (
	"os"
	"strings"
	"testing"
)

func TestLoginPATHReadsTheRegistryOnWindows(t *testing.T) {
	machine, _ := registryPATH()
	if machine == "" || strings.Contains(machine, "%SystemRoot%") {
		t.Fatalf("machine Path = %q, want it read and expanded", machine)
	}
	host := `C:\agent-pane-test-only`
	t.Setenv("PATH", os.Getenv("PATH")+";"+host)
	got := loginPATH()
	first, _, _ := strings.Cut(machine, ";")
	if !strings.HasPrefix(strings.ToLower(got), strings.ToLower(first)) {
		t.Errorf("loginPATH = %q, want it to start with the machine Path", got)
	}
	if !strings.HasSuffix(got, ";"+host) {
		t.Errorf("loginPATH = %q, want the host's own entries last", got)
	}
}
