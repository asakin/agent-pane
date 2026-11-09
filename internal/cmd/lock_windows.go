//go:build windows

package cmd

import (
	"context"
	"errors"
)

// lockFile is not implemented on Windows, where the bundled core does not
// run.
func lockFile(ctx context.Context, path string) (func(), error) {
	return nil, errors.New("file locks are not supported on Windows")
}
