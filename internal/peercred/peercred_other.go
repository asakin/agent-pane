//go:build !linux && !darwin

package peercred

import "errors"

var errUnsupported = errors.New("peer credentials are not supported on this platform")

type process struct{}

func peerOf(int) (Peer, *process, error)     { return Peer{}, nil, errUnsupported }
func processOf(int) (*process, error)        { return nil, errUnsupported }
func (*process) close()                      {}
func (*process) pid() int                    { return 0 }
func (*process) ancestryErr() error          { return errUnsupported }
func (*process) alive() error                { return errUnsupported }
func (*process) executable() (string, error) { return "", errUnsupported }
func (*process) parent() (*process, error)   { return nil, errUnsupported }
