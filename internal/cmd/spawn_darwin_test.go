//go:build darwin

package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aymanbagabas/go-pty"
)

func TestSpawnChildParentIsHelper(t *testing.T) {
	sock := filepath.Join("/tmp", "at-spawn-"+strconv.Itoa(os.Getpid())+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	helper := exec.Command(os.Args[0], "-test.run=^TestSpawnHelperProcess$")
	helper.Env = append(os.Environ(), "SPAWN_SOCK="+sock)
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = helper.Process.Kill()
		_, _ = helper.Process.Wait()
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("spawn socket did not appear")
		}
		time.Sleep(20 * time.Millisecond)
	}

	tty, err := pty.New()
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()

	marker := filepath.Join(t.TempDir(), "ran")
	out := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 128)
		n, _ := tty.Read(buf)
		out <- buf[:n]
	}()
	cmd := tty.Command("/bin/sh", "-c", "echo hello-from-child; ps -o ppid= -p $$ > "+marker)
	wait, err := dialSpawn(sock, tty, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("ppid %q: %v", raw, err)
	}
	if ppid != helper.Process.Pid {
		t.Fatalf("parent %d, helper %d", ppid, helper.Process.Pid)
	}
	select {
	case b := <-out:
		if !strings.Contains(string(b), "hello-from-child") {
			t.Fatalf("tty output %q", b)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("child wrote nothing to the terminal")
	}
}

func TestSpawnHelperProcess(t *testing.T) {
	sock := os.Getenv("SPAWN_SOCK")
	if sock == "" {
		t.Skip()
	}
	if err := serveSpawnSocket(context.Background(), sock); err != nil {
		t.Fatal(err)
	}
}
