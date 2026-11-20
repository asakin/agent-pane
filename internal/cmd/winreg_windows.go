package cmd

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// hkcuRegistry is userRegistry on HKEY_CURRENT_USER. With a prefix, every key,
// Environment included, sits under HKCU\<prefix>, so tests do not touch the
// real browser keys or the real user Path.
type hkcuRegistry struct {
	prefix string
}

func newUserRegistry() userRegistry { return hkcuRegistry{} }

func (r hkcuRegistry) path(key string) string {
	if r.prefix == "" {
		return key
	}
	return r.prefix + `\` + key
}

func (r hkcuRegistry) DefaultValue(key string) (string, bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, r.path(key), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("")
	if errors.Is(err, registry.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (r hkcuRegistry) SetDefaultValue(key, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, r.path(key), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("", value)
}

func (r hkcuRegistry) UserPath() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, r.path("Environment"), registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	return v, err
}

func (r hkcuRegistry) SetUserPath(value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, r.path("Environment"), registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if _, kind, err := k.GetStringValue("Path"); err == nil && kind == registry.SZ {
		return k.SetStringValue("Path", value)
	}
	return k.SetExpandStringValue("Path", value)
}

var procSendMessageTimeout = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

func (r hkcuRegistry) EnvironmentChanged() {
	if r.prefix != "" {
		return
	}
	const (
		hwndBroadcast    = 0xffff
		wmSettingChange  = 0x001a
		smtoAbortIfHung  = 0x0002
		broadcastTimeout = 5000
	)
	env, err := syscall.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	procSendMessageTimeout.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(env)),
		smtoAbortIfHung, broadcastTimeout, uintptr(unsafe.Pointer(&result)))
}
