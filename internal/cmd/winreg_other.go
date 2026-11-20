//go:build !windows

package cmd

// newUserRegistry has no registry to give outside Windows; nothing there
// reads or writes one.
func newUserRegistry() userRegistry { return nil }
