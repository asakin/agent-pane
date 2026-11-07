//go:build !unix

package jsonrpc

import (
	"io"
	"os"
)

// nativeOutput returns stdout. Its writes cannot be interrupted, so a
// message whose write has started is always finished.
func nativeOutput() io.Writer {
	return os.Stdout
}
