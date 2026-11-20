//go:build darwin

package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"agent-pane/internal/peercred"
	"github.com/aymanbagabas/go-pty"
)

// startSpawnHelper runs serveSpawnSocket in a child test process. A non-empty
// allow replaces the executable the helper accepts.
func startSpawnHelper(t *testing.T, name, allow string) (string, *exec.Cmd) {
	t.Helper()
	// The helper makes the socket's folder 0700, so give it its own. Short,
	// because unix socket paths are limited to 104 bytes.
	dir, err := os.MkdirTemp("/tmp", "at-"+name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// Start from an older install's 0755 folder; the helper must tighten it.
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s.sock")

	helper := exec.Command(os.Args[0], "-test.run=^TestSpawnHelperProcess$")
	helper.Env = append(os.Environ(), "SPAWN_SOCK="+sock, "SPAWN_ALLOW="+allow)
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_, _ = helper.Process.Wait()
	})

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
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("socket folder mode: %v %v", info.Mode().Perm(), err)
	}
	return sock, helper
}

func TestSpawnRefusesOtherExecutable(t *testing.T) {
	sock, _ := startSpawnHelper(t, "spawn-refuse", "/usr/bin/true")

	tty, err := pty.New()
	if err != nil {
		t.Fatal(err)
	}
	defer tty.Close()

	marker := filepath.Join(t.TempDir(), "ran")
	cmd := tty.Command("/bin/sh", "-c", "touch "+marker)
	if _, _, err := dialSpawn(sock, tty, cmd, nil); err == nil {
		t.Fatal("spawn accepted a client that is not the allowed executable")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("spawn ran the command for a refused client")
	}
}

func TestSpawnChildParentIsHelper(t *testing.T) {
	sock, helper := startSpawnHelper(t, "spawn", "")

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
	wait, _, err := dialSpawn(sock, tty, cmd, nil)
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
	var err error
	if allow := os.Getenv("SPAWN_ALLOW"); allow != "" {
		err = serveSpawnSocketFor(context.Background(), sock, peercred.Policy{Executables: []string{allow}})
	} else {
		err = serveSpawnSocket(context.Background(), sock)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestSpawnRunsWithoutATerminal(t *testing.T) {
	sock, _ := startSpawnHelper(t, "spawn-run", "")

	cmd := exec.Command("/bin/sh", "-c", `echo "$GREETING from $(pwd -P)"; [ "$(ps -o pgid= -p $$ | tr -d ' ')" = $$ ] && echo own session; exit 3`)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd.Dir = dir
	cmd.Env = []string{"GREETING=hello", "PATH=/usr/bin:/bin"}
	out, ran, err := runSpawn(context.Background(), sock, cmd)
	if !ran {
		t.Fatalf("spawn agent did not run the command: %v", err)
	}
	if err == nil || err.Error() != "exit status 3" {
		t.Fatalf("err = %v, want exit status 3", err)
	}
	if got, want := string(out), "hello from "+dir+"\nown session\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestSpawnRunUnavailable(t *testing.T) {
	_, ran, err := runSpawn(context.Background(), filepath.Join(t.TempDir(), "none.sock"), exec.Command("/usr/bin/true"))
	if ran || err == nil {
		t.Fatalf("ran = %v, err = %v; want not run", ran, err)
	}
}

func TestSpawnRunWithoutAnAnswerCountsAsRun(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "at-noack")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// An agent that takes the request and is gone before it answers may
	// have started the command.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = bufio.NewReader(conn).ReadString('\n')
		conn.Close()
	}()
	_, ran, err := runSpawn(context.Background(), sock, exec.Command("/usr/bin/true"))
	if !ran || err == nil {
		t.Fatalf("ran = %v, err = %v; want run with an error", ran, err)
	}
}

func TestSpawnRunStopsWhenTheCallerCancels(t *testing.T) {
	sock, _ := startSpawnHelper(t, "spawn-cancel", "")

	pidFile := filepath.Join(t.TempDir(), "pid")
	cmd := exec.Command("/bin/sh", "-c", "echo $$ > "+pidFile+"; exec sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := runSpawn(ctx, sock, cmd)
		done <- err
	}()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(pidFile); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				pid = n
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the command did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("runSpawn returned no error after its context was cancelled")
	}
	deadline = time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the command (pid %d) still runs after its caller cancelled", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSpawnTerminalRequestNeedsATerminal(t *testing.T) {
	sock, _ := startSpawnHelper(t, "spawn-noterm", "")

	marker := filepath.Join(t.TempDir(), "ran")
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(spawnRequest{Args: []string{"/usr/bin/touch", marker}}); err != nil {
		t.Fatal(err)
	}
	var ack spawnAck
	if err := json.NewDecoder(conn).Decode(&ack); err != nil {
		t.Fatal(err)
	}
	if ack.OK || ack.Error != "missing terminal" {
		t.Fatalf("ack = %+v, want missing terminal", ack)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("spawn ran a terminal request that came with no terminal")
	}
}
