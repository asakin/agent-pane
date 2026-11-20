package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// testRegistry is hkcuRegistry under a fresh HKCU\Software\agent-pane-test-*
// key, removed when the test ends; the real browser keys and user Path are
// not touched.
func testRegistry(t *testing.T) hkcuRegistry {
	t.Helper()
	b := make([]byte, 6)
	rand.Read(b)
	prefix := `Software\agent-pane-test-` + hex.EncodeToString(b)
	t.Cleanup(func() { deleteKeyTree(registry.CURRENT_USER, prefix) })
	return hkcuRegistry{prefix: prefix}
}

func deleteKeyTree(root registry.Key, path string) {
	k, err := registry.OpenKey(root, path, registry.ENUMERATE_SUB_KEYS)
	if err == nil {
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, name := range names {
			deleteKeyTree(root, path+`\`+name)
		}
	}
	registry.DeleteKey(root, path)
}

func TestPlaceWindowsRegistry(t *testing.T) {
	l := windowsTestLayout(t)
	src := t.TempDir()
	writeWindowsRelease(t, src)
	reg := testRegistry(t)

	// An existing REG_EXPAND_SZ user Path keeps its type and entries.
	k, _, err := registry.CreateKey(registry.CURRENT_USER, reg.path("Environment"), registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.SetExpandStringValue("Path", `%SystemRoot%\system32`); err != nil {
		t.Fatal(err)
	}
	k.Close()

	placeOnce(t, l, src, reg)
	chrome := browserDirs("windows", l.home, l.localAppData)[0]
	if got, ok, err := reg.DefaultValue(chrome.key); err != nil || !ok || got != l.manifest {
		t.Errorf("Chrome's key = %q, %v, %v; want %q", got, ok, err, l.manifest)
	}
	k, err = registry.OpenKey(registry.CURRENT_USER, reg.path("Environment"), registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	path, kind, err := k.GetStringValue("Path")
	k.Close()
	if want := `%SystemRoot%\system32;` + filepath.Dir(l.binary); err != nil || path != want || kind != registry.EXPAND_SZ {
		t.Errorf("user Path = %q (type %d), %v; want %q as REG_EXPAND_SZ", path, kind, err, want)
	}

	if out, errOut := placeOnce(t, l, src, reg); out != "" || errOut != "" {
		t.Errorf("second run printed:\n%s%s", out, errOut)
	}
}

// TestPlaceWindowsHelperSleep is the running program
// TestPlaceFileReplacesARunningProgram replaces.
func TestPlaceWindowsHelperSleep(t *testing.T) {
	if os.Getenv("AGENT_PANE_TEST_SLEEP") != "1" {
		t.Skip("helper process")
	}
	time.Sleep(time.Minute)
}

func TestPlaceFileReplacesARunningProgram(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "bin", "running.exe")
	if _, err := placeFile(self, dest); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(dest, "-test.run=^TestPlaceWindowsHelperSleep$")
	c.Env = append(os.Environ(), "AGENT_PANE_TEST_SLEEP=1")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c.Process.Kill()
		c.Wait()
	}()
	// Give Windows time to map the image.
	time.Sleep(500 * time.Millisecond)
	if err := os.Remove(dest); err == nil {
		t.Skip("this Windows lets a running program be deleted")
	}

	newer := filepath.Join(dir, "newer.exe")
	putFile(t, newer, "a newer build")
	changed, err := placeFile(newer, dest)
	if err != nil || !changed {
		t.Fatalf("placeFile over a running program = %v, %v", changed, err)
	}
	if got := readString(t, dest); got != "a newer build" {
		t.Errorf("dest = %q", got)
	}
	if !exists(dest + ".old-1") {
		t.Fatal("the running copy was not kept aside")
	}
	c.Process.Kill()
	c.Wait()
	eventually(t, "the old copy to be removed", func() bool {
		removeOldCopies(filepath.Dir(dest))
		_, err := os.Lstat(dest + ".old-1")
		return errors.Is(err, os.ErrNotExist)
	})
}
