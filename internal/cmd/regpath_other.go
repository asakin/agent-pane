//go:build !windows

package cmd

// registryPATH has no registry to read outside Windows.
func registryPATH() (machine, user string) { return "", "" }
