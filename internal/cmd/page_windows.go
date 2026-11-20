//go:build windows

package cmd

import (
	"errors"
	"net"
	"os"
)

// Page reading on Windows needs its own page endpoint and peer check, which
// is a later change (RFC 0002, Page access). Until then every page path
// refuses with errPageWindows.
const errPageWindows = "page reading is not available on Windows yet"

func newPageChannel() (*net.UnixConn, *os.File, error) {
	return nil, nil, errors.New(errPageWindows)
}

func (s *Session) servePage(*net.UnixConn, func() extensionCaller) {}

func pageClient(string, pageRequest) (string, error) {
	return "", errors.New(errPageWindows)
}

func newPageSocket() (*net.UnixListener, string, error) {
	return nil, "", errors.New(errPageWindows)
}

func listenPageSocket(string) (*net.UnixListener, error) {
	return nil, errors.New(errPageWindows)
}

func (s *Session) servePageSocketAt(string, int, func() extensionCaller) {}

func (s *Session) servePageSocket(*net.UnixListener, int, func() extensionCaller) {}

func readPage(string, string, pageRequest) (string, error) {
	return "", errors.New(errPageWindows)
}
