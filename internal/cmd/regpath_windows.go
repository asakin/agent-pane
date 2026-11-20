package cmd

import "golang.org/x/sys/windows/registry"

// registryPATH returns the machine and user Path from the registry, with
// %NAME% references expanded: what a program started now would get, where
// the host's own PATH is the one the browser had when it started.
func registryPATH() (machine, user string) {
	machine = readPathValue(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`)
	user = readPathValue(registry.CURRENT_USER, `Environment`)
	return machine, user
}

// readPathValue is the expanded Path value under root\key, or "" when it is
// missing or unreadable.
func readPathValue(root registry.Key, key string) string {
	k, err := registry.OpenKey(root, key, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, kind, err := k.GetStringValue("Path")
	if err != nil {
		return ""
	}
	if kind == registry.EXPAND_SZ {
		if expanded, err := registry.ExpandString(v); err == nil {
			return expanded
		}
	}
	return v
}
