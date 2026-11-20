package cmd

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestLockFileExcludesConcurrentHolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "team.lock")
	ctx := context.Background()
	unlock, err := lockFile(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := lockFile(short, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock while held: %v", err)
	}
	unlock()
	again, err := lockFile(ctx, path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()
}

func TestLockFileWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panes.json.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := lockFile(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		again, err := lockFile(ctx, path)
		if err == nil {
			again()
		}
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("second lock returned while the first was held: %v", err)
	case <-time.After(60 * time.Millisecond):
	}
	unlock()
	if err := <-got; err != nil {
		t.Fatalf("second lock after release: %v", err)
	}
}
