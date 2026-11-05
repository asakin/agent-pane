//go:build darwin

package peercred

import (
	"errors"
	"os"
	"testing"
)

// A process held with an older pid version, as when its pid is reused or it
// execs, is refused.
func TestStaleVersionIsRefused(t *testing.T) {
	proc, err := processOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proc.executable(); err != nil {
		t.Fatal(err)
	}
	stale := *proc
	stale.version--
	if _, err := stale.executable(); !errors.Is(err, errChanged) {
		t.Fatalf("executable with a stale pid version: err %v", err)
	}
	if descends(&stale, os.Getppid()) {
		t.Fatal("ancestry passed with a stale pid version")
	}
	if descends(&stale, os.Getpid()) {
		t.Fatal("a stale pid version matched itself as the ancestor")
	}
	reused := *proc
	reused.uniqueID++
	if _, err := reused.executable(); !errors.Is(err, errChanged) {
		t.Fatalf("executable with another unique id: err %v", err)
	}
}

func TestPeerTokenNamesThisProcess(t *testing.T) {
	server, _ := connPair(t)
	_, proc, err := get(server)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := processOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if *proc != *mine {
		t.Fatalf("token process %+v, want %+v", *proc, *mine)
	}
}
