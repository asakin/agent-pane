//go:build darwin

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"

	"github.com/aymanbagabas/go-pty"
	"github.com/spf13/cobra"
)

type spawnRequest struct {
	Path string   `json:"path"`
	Args []string `json:"args"`
	Env  []string `json:"env"`
	Dir  string   `json:"dir"`
}

type spawnAck struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type spawnExit struct {
	Exit int `json:"exit"`
}

func spawnSocketPath() string {
	return dataDir + "/spawn.sock"
}

func NewCmdSpawn() *cobra.Command {
	return &cobra.Command{
		Use:    "spawn",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return serveSpawnSocket(context.Background(), spawnSocketPath())
		},
	}
}

func startPty(tty pty.Pty, cmd *pty.Cmd) (func() error, error) {
	return dialSpawn(spawnSocketPath(), tty, cmd)
}

func dialSpawn(path string, tty pty.Pty, cmd *pty.Cmd) (func() error, error) {
	// The helper writes the child's stdout through this fd, so it has to be
	// writable. O_NOCTTY keeps the host from adopting the slave.
	slave, err := os.OpenFile(tty.Name(), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, err
	}
	defer slave.Close()

	conn, err := net.Dial("unix", path)
	if err != nil {
		return nil, fmt.Errorf("spawn agent is not running; re-run the installer")
	}
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("spawn agent is not running; re-run the installer")
	}

	rights := syscall.UnixRights(int(slave.Fd()))
	if _, _, err := uc.WriteMsgUnix([]byte{0}, rights, nil); err != nil {
		conn.Close()
		return nil, err
	}

	argv := cmd.Args
	if len(argv) == 0 {
		argv = []string{cmd.Path}
	}
	if err := json.NewEncoder(conn).Encode(spawnRequest{
		Path: cmd.Path,
		Args: argv,
		Env:  cmd.Env,
		Dir:  cmd.Dir,
	}); err != nil {
		conn.Close()
		return nil, err
	}

	dec := json.NewDecoder(conn)
	var ack spawnAck
	if err := dec.Decode(&ack); err != nil {
		conn.Close()
		return nil, err
	}
	if !ack.OK {
		conn.Close()
		if ack.Error == "" {
			ack.Error = "spawn agent did not start the shell"
		}
		return nil, fmt.Errorf("%s", ack.Error)
	}

	return func() error {
		defer conn.Close()
		var done spawnExit
		if err := dec.Decode(&done); err != nil {
			return err
		}
		if done.Exit != 0 {
			return fmt.Errorf("exit status %d", done.Exit)
		}
		return nil
	}, nil
}

func serveSpawnSocket(ctx context.Context, path string) error {
	if err := os.MkdirAll(dirOf(path), 0700); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	_ = os.Chmod(path, 0600)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		uc, ok := conn.(*net.UnixConn)
		if !ok {
			conn.Close()
			continue
		}
		go handleSpawnConn(uc)
	}
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

func handleSpawnConn(conn *net.UnixConn) {
	defer conn.Close()
	buf := make([]byte, 1)
	oob := make([]byte, syscall.CmsgSpace(4))
	_, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return
	}
	scms, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(scms) == 0 {
		_ = json.NewEncoder(conn).Encode(spawnAck{Error: "missing terminal"})
		return
	}
	fds, err := syscall.ParseUnixRights(&scms[0])
	if err != nil || len(fds) == 0 {
		_ = json.NewEncoder(conn).Encode(spawnAck{Error: "missing terminal"})
		return
	}
	slave := os.NewFile(uintptr(fds[0]), "slave")
	defer slave.Close()

	var req spawnRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(spawnAck{Error: err.Error()})
		return
	}
	argv := req.Args
	if len(argv) == 0 {
		_ = json.NewEncoder(conn).Encode(spawnAck{Error: "missing command"})
		return
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if req.Path != "" {
		cmd.Path = req.Path
	}
	cmd.Env = req.Env
	cmd.Dir = req.Dir
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		_ = json.NewEncoder(conn).Encode(spawnAck{Error: err.Error()})
		return
	}
	if err := json.NewEncoder(conn).Encode(spawnAck{OK: true}); err != nil {
		_ = cmd.Process.Kill()
		return
	}
	err = cmd.Wait()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
		}
	}
	_ = json.NewEncoder(conn).Encode(spawnExit{Exit: code})
}
