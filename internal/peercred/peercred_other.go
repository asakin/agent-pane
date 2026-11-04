//go:build !linux && !darwin

package peercred

import "errors"

var errUnsupported = errors.New("peer credentials are not supported on this platform")

func peerOf(int) (Peer, error)       { return Peer{}, errUnsupported }
func executable(int) (string, error) { return "", errUnsupported }
func parentOf(int) (int, error)      { return 0, errUnsupported }
