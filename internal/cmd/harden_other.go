//go:build !linux

package cmd

// hardenHost is a no-op: macOS has no /proc, and its task ports need
// debugger rights.
func hardenHost() error { return nil }
