//go:build windows

package cmd

import (
	"errors"
	"net"
	"os"
)

func newPageChannel() (*net.UnixConn, *os.File, error) {
	return nil, nil, errors.New("page reading is not available on Windows")
}

func (s *Session) servePage(*net.UnixConn, func() extensionCaller) {}

func pageClient(string, pageRequest) (string, error) {
	return "", errors.New(errNotInTerminal)
}
