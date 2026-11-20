package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// userRegistry is the part of HKEY_CURRENT_USER the Windows placing step and
// doctor use. Keys are relative to HKEY_CURRENT_USER. The Windows
// implementation (winreg_windows.go) can put every key under a test subkey;
// tests elsewhere use a map.
type userRegistry interface {
	// DefaultValue returns key's default value; ok is false when the key or
	// its default value does not exist.
	DefaultValue(key string) (value string, ok bool, err error)
	// SetDefaultValue creates key when needed and sets its default value.
	SetDefaultValue(key, value string) error
	// UserPath returns the user Path in HKCU\Environment, not expanded, or ""
	// when it is not set.
	UserPath() (string, error)
	// SetUserPath writes the user Path, keeping its value type, and
	// REG_EXPAND_SZ when it is new.
	SetUserPath(value string) error
	// EnvironmentChanged tells running programs that the user environment
	// changed (WM_SETTINGCHANGE), so terminals opened afterwards see Path.
	EnvironmentChanged()
}

const (
	managedStart    = "<!-- agent-pane:start -->"
	managedEnd      = "<!-- agent-pane:end -->"
	skillMarkerText = "Installed by Agent Pane; replaced on every install. Delete this folder to remove the skill.\n"
)

func NewCmdPlace() *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:    "place",
		Hidden: true,
		Short:  "Put a Windows release folder in place (run by install.ps1 and doctor --fix)",
		Long: `Puts the Windows release in the folder this program sits in (or --from) in
place under %LOCALAPPDATA%\agent-pane, registers the native-messaging host for
each installed browser, adds the program to the user Path, and runs doctor.
It compares before it writes and prints a line only for what it changed.
On macOS and Linux the release's place.sh does this instead.`,
		Args:              cobra.NoArgs,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			if runtime.GOOS != "windows" {
				return errors.New("agent-pane place is only used on Windows; on macOS and Linux the release's place.sh puts it in place")
			}
			src := from
			if src == "" {
				exe, err := os.Executable()
				if err != nil {
					return fmt.Errorf("could not find this program's folder: %w", err)
				}
				src = filepath.Dir(exe)
			}
			src, err := filepath.Abs(src)
			if err != nil {
				return err
			}
			l := currentLayout()
			if l.home == "" || l.localAppData == "" {
				return errors.New("USERPROFILE and LOCALAPPDATA must be set")
			}
			p := &placer{
				l: l, src: src, reg: newUserRegistry(), getenv: os.Getenv,
				out: cmd.OutOrStdout(), errOut: cmd.ErrOrStderr(),
				runInstalled: func(args ...string) (string, error) {
					out, err := exec.Command(l.binary, args...).CombinedOutput()
					return string(out), err
				},
			}
			return p.run()
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "the release folder to place (default: the folder this program is in)")
	_ = cmd.Flags().MarkHidden("from")
	return cmd
}

// placer puts a Windows release folder in place, as scripts/place.sh does on
// macOS and Linux. Its file logic runs on every OS so it is tested
// everywhere; only reg talks to the Windows registry.
type placer struct {
	l      installLayout
	src    string // the release folder to place
	reg    userRegistry
	getenv func(string) string
	out    io.Writer
	errOut io.Writer
	// runInstalled runs the placed binary with args and returns its output.
	runInstalled func(args ...string) (string, error)
	changed      bool
}

func (p *placer) updated(what string) {
	fmt.Fprintf(p.out, "agent-pane: updated %s\n", p.l.show(what))
	p.changed = true
}

func (p *placer) warn(format string, args ...any) {
	fmt.Fprintf(p.errOut, "agent-pane: "+format+"\n", args...)
}

func (p *placer) run() error {
	steps := []func() error{
		p.keepRelease,
		p.placeBinary,
		p.placeCore,
		p.syncCoreLicenses,
		p.writeManifest,
		p.ensurePath,
		p.writeInstructions,
		p.syncAgents,
		p.syncSkill,
		p.syncExtension,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return p.finish()
}

// finish runs doctor on the placed binary. A run that changed nothing and
// passes prints nothing; otherwise doctor's report is shown.
func (p *placer) finish() error {
	report, err := p.runInstalled("doctor")
	if err != nil {
		io.WriteString(p.out, report)
		return errors.New("the installation has problems; see above")
	}
	if !p.changed {
		return nil
	}
	io.WriteString(p.out, report)
	version, err := p.runInstalled("--version")
	if err != nil {
		return fmt.Errorf("%s --version: %v", p.l.binary, err)
	}
	fmt.Fprintf(p.out, "agent-pane: installed %s\n", strings.TrimSpace(version))
	fmt.Fprintf(p.out, "agent-pane: in Chrome or Edge, load the extension once with Load unpacked from %s (chrome://extensions, Developer mode); after an update, click its reload button\n",
		filepath.Join(p.l.dataDir, "extensions", "chrome"))
	return nil
}

// checkWindowsRelease is place.sh's check_layout for the Windows archive.
func checkWindowsRelease(src string) error {
	need := []struct{ rel, what string }{
		{"agent-pane.exe", "agent-pane.exe"},
		{"agents/enabled.json", "agent catalog"},
		{"skills/agent-pane/SKILL.md", "agent-pane skill"},
		{"instructions.md", "instructions.md"},
		{"extensions/chrome/manifest.json", "Chrome extension"},
		{"libexec/hc.exe", `bundled herdr-core (libexec\hc.exe)`},
		{"herdr-core.lock", "herdr-core.lock"},
		{"licenses/herdr-core/LICENSE", "herdr-core license"},
	}
	for _, n := range need {
		info, err := os.Stat(filepath.Join(src, filepath.FromSlash(n.rel)))
		if err != nil || info.IsDir() {
			return fmt.Errorf("%s has no %s", src, n.what)
		}
		if info.Size() == 0 && n.rel != "agents/enabled.json" && n.rel != "extensions/chrome/manifest.json" {
			return fmt.Errorf("%s has no %s", src, n.what)
		}
	}
	if !nonEmptyNotices(filepath.Join(src, "licenses", "herdr-core", "THIRD-PARTY-NOTICES"), true) {
		return fmt.Errorf("%s has no herdr-core third-party notices", src)
	}
	return nil
}

// nonEmptyNotices reports whether path is a non-empty file or, when folders
// are allowed (Windows), a folder of only non-empty files with at least one.
func nonEmptyNotices(path string, folders bool) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return info.Size() > 0
	}
	if !folders {
		return false
	}
	files := 0
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if fi.Size() == 0 {
			return errors.New("empty")
		}
		files++
		return nil
	})
	return err == nil && files > 0
}

// keepRelease makes release a copy of src, so doctor can compare against it
// and doctor --fix can place it again.
func (p *placer) keepRelease() error {
	if err := checkWindowsRelease(p.src); err != nil {
		return err
	}
	release := p.l.release
	if err := os.MkdirAll(p.l.dataDir, 0o755); err != nil {
		return err
	}
	os.RemoveAll(release + ".old")
	if samePath(p.l.goos, p.src, release) {
		return nil
	}
	if same, _ := sameTree(p.src, release); same {
		return nil
	}
	if err := os.RemoveAll(release + ".new"); err != nil {
		return err
	}
	if err := copyTree(p.src, release+".new"); err != nil {
		return fmt.Errorf("could not copy the release to %s: %w", release, err)
	}
	if _, err := os.Stat(release); err == nil {
		if err := os.Rename(release, release+".old"); err != nil {
			return fmt.Errorf("could not move the old release aside: %w", err)
		}
	}
	if err := os.Rename(release+".new", release); err != nil {
		return fmt.Errorf("could not install the release at %s: %w", release, err)
	}
	// A file still open in the old copy keeps it; the next run removes it.
	os.RemoveAll(release + ".old")
	p.updated(release)
	return nil
}

func (p *placer) placeBinary() error {
	dir := filepath.Dir(p.l.binary)
	removeOldCopies(dir)
	changed, err := placeFile(p.l.releaseBinary(), p.l.binary)
	if err != nil {
		return fmt.Errorf("could not install the binary: %w", err)
	}
	if changed {
		p.updated(p.l.binary)
	}
	return nil
}

// placeCore installs the release's whole libexec tree: hc.exe and the ConPTY
// files it loads, each replaced with rename-aside since a running core keeps
// them open.
func (p *placer) placeCore() error {
	from := filepath.Join(p.l.release, "libexec")
	to := filepath.Join(p.l.dataDir, "libexec")
	removeOldCopies(to)
	want := map[string]bool{}
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		want[rel] = true
		dest := filepath.Join(to, rel)
		changed, err := placeFile(path, dest)
		if err != nil {
			return fmt.Errorf("could not install %s: %w", dest, err)
		}
		if changed {
			p.updated(dest)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Files the release no longer has: removed, or moved aside when in use.
	return filepath.WalkDir(to, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(to, path)
		if want[rel] || oldCopy.MatchString(d.Name()) {
			return nil
		}
		if os.Remove(path) != nil {
			if _, err := moveAside(path); err != nil {
				return nil
			}
		}
		p.updated(path)
		return nil
	})
}

func (p *placer) syncCoreLicenses() error {
	return p.sync(filepath.Join(p.l.release, "licenses", "herdr-core"), filepath.Join(p.l.dataDir, "licenses", "herdr-core"))
}

func (p *placer) syncAgents() error {
	return p.sync(filepath.Join(p.l.release, "agents"), filepath.Join(p.l.dataDir, "agents"))
}

func (p *placer) syncExtension() error {
	return p.sync(filepath.Join(p.l.release, "extensions", "chrome"), filepath.Join(p.l.dataDir, "extensions", "chrome"))
}

func (p *placer) sync(from, to string) error {
	changed, err := syncTree(from, to)
	if err != nil {
		return err
	}
	if changed {
		p.updated(to)
	}
	return nil
}

// windowsManifest is the native-messaging manifest file, in place.sh's
// format, pointing at the binary itself.
func windowsManifest(binary string) []byte {
	m := struct {
		Name           string   `json:"name"`
		Description    string   `json:"description"`
		Type           string   `json:"type"`
		Path           string   `json:"path"`
		AllowedOrigins []string `json:"allowed_origins"`
	}{nativeHostName, nativeHostSummary, "stdio", binary, []string{extensionOrigin}}
	data, _ := json.MarshalIndent(m, "", "    ")
	return append(data, '\n')
}

// writeManifest writes the manifest file and points each installed browser's
// registry key at it.
func (p *placer) writeManifest() error {
	changed, err := writeIfChanged(p.l.manifest, windowsManifest(p.l.binary))
	if err != nil {
		return err
	}
	if changed {
		p.updated(p.l.manifest)
	}
	count := 0
	for _, b := range browserDirs(p.l.goos, p.l.home, p.l.localAppData) {
		if info, err := os.Stat(b.parent); err != nil || !info.IsDir() {
			continue
		}
		count++
		got, ok, err := p.reg.DefaultValue(b.key)
		if err != nil {
			return fmt.Errorf(`could not read HKCU\%s: %w`, b.key, err)
		}
		if ok && got == p.l.manifest {
			continue
		}
		if err := p.reg.SetDefaultValue(b.key, p.l.manifest); err != nil {
			return fmt.Errorf(`could not write HKCU\%s: %w`, b.key, err)
		}
		p.updated(`HKCU\` + b.key)
	}
	if count == 0 {
		return errors.New("no browser user data folder was found, so the native messaging host was not registered")
	}
	return nil
}

// ensurePath adds the binary's folder to the user Path.
func (p *placer) ensurePath() error {
	binDir := filepath.Dir(p.l.binary)
	current, err := p.reg.UserPath()
	if err != nil {
		return fmt.Errorf("could not read the user Path: %w", err)
	}
	if pathListHas(p.l.goos, current, binDir, p.getenv) {
		return nil
	}
	next := strings.TrimRight(current, ";")
	if next != "" {
		next += ";"
	}
	next += binDir
	if err := p.reg.SetUserPath(next); err != nil {
		return fmt.Errorf("could not add %s to the user Path: %w", binDir, err)
	}
	p.reg.EnvironmentChanged()
	p.updated("the user Path (added " + binDir + "; open a new terminal)")
	return nil
}

func (p *placer) writeInstructions() error {
	data, err := os.ReadFile(filepath.Join(p.l.release, "instructions.md"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.l.configDir, 0o755); err != nil {
		return err
	}
	agents := filepath.Join(p.l.configDir, "AGENTS.md")
	changed, err := writeIfChanged(agents, data)
	if err != nil {
		return err
	}
	if changed {
		p.updated(agents)
	}
	if err := p.upsertManaged(filepath.Join(p.l.home, "AGENTS.md"), strings.TrimRight(string(data), "\n")); err != nil {
		return err
	}
	return p.upsertManaged(filepath.Join(p.l.home, "CLAUDE.md"), managedStart+"\n@AGENTS.md\n"+managedEnd)
}

// upsertManaged replaces the Agent Pane block in a file the person owns, and
// creates the file when it does not exist, as place.sh's upsert_managed does.
func (p *placer) upsertManaged(file, body string) error {
	info, err := os.Lstat(file)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(file, []byte(body+"\n"), 0o644); err != nil {
			return err
		}
		p.updated(file)
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		p.warn("%s is a symlink; left unchanged", p.l.show(file))
		return nil
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if !bytes.Contains(data, []byte(managedStart)) {
		p.warn("%s has no Agent Pane block; left unchanged", p.l.show(file))
		return nil
	}
	if !bytes.Contains(data, []byte(managedEnd)) {
		return fmt.Errorf("%s has a start marker and no end marker", file)
	}
	changed, err := writeIfChanged(file, []byte(replaceManagedBlock(string(data), body)))
	if err != nil {
		return err
	}
	if changed {
		p.updated(file)
	}
	return nil
}

// replaceManagedBlock is place.sh's awk program: each line equal to the start
// marker, through the next line equal to the end marker, becomes body. A
// trailing carriage return is ignored when matching the markers.
func replaceManagedBlock(text, body string) string {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	var b strings.Builder
	replacing := false
	for _, line := range lines {
		key := strings.TrimSuffix(line, "\r")
		switch {
		case !replacing && key == managedStart:
			b.WriteString(body + "\n")
			replacing = true
		case replacing && key == managedEnd:
			replacing = false
		case replacing:
		default:
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// syncSkill installs the canonical skill and each coding agent's copy of it;
// a copy is ours when it holds the marker file, and only ours are replaced.
func (p *placer) syncSkill() error {
	canonical := filepath.Join(p.l.dataDir, "skills", "agent-pane")
	if err := p.sync(filepath.Join(p.l.release, "skills", "agent-pane"), canonical); err != nil {
		return err
	}
	for _, dir := range []string{".agents", ".claude"} {
		if err := p.installSkillCopy(canonical, filepath.Join(p.l.home, dir, "skills", "agent-pane")); err != nil {
			return err
		}
	}
	return nil
}

func (p *placer) installSkillCopy(from, to string) error {
	tmp := filepath.Join(p.l.dataDir, "skills", ".agent-pane-copy")
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if info, err := os.Lstat(to); err == nil {
		if _, markerErr := os.Stat(filepath.Join(to, skillMarker)); info.Mode()&fs.ModeSymlink != 0 || markerErr != nil {
			p.warn("%s is not Agent Pane's; left unchanged", p.l.show(to))
			return nil
		}
	}
	if same, _ := sameTree(from, to); same {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	// Built in the data folder, marker first, then swapped in, so a run cut
	// short never leaves an unmarked copy.
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, skillMarker), []byte(skillMarkerText), 0o644); err != nil {
		return err
	}
	if err := copyTree(from, tmp); err != nil {
		return err
	}
	if err := os.RemoveAll(to); err != nil {
		return err
	}
	if err := os.Rename(tmp, to); err != nil {
		return err
	}
	p.updated(to)
	return nil
}

// writeIfChanged writes data to path through a temporary file when the
// content differs, and reports whether it wrote.
func writeIfChanged(path string, data []byte) (bool, error) {
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-pane.*")
	if err != nil {
		return false, fmt.Errorf("could not write in %s: %w", filepath.Dir(path), err)
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return false, fmt.Errorf("could not write %s: %v", path, errors.Join(werr, cerr))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return false, fmt.Errorf("could not write %s: %w", path, err)
	}
	return true, nil
}

// oldCopy matches the names moveAside gives replaced files.
var oldCopy = regexp.MustCompile(`\.old-[0-9]+$`)

// moveAside renames path to the first free <path>.old-<n>. Windows lets a
// running program's file be renamed but not overwritten or deleted.
func moveAside(path string) (string, error) {
	for n := 1; ; n++ {
		old := path + ".old-" + strconv.Itoa(n)
		if _, err := os.Lstat(old); errors.Is(err, fs.ErrNotExist) {
			return old, os.Rename(path, old)
		}
	}
}

// removeOldCopies deletes the files moveAside left under dir that are no
// longer in use; the rest wait for the next run.
func removeOldCopies(dir string) {
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && oldCopy.MatchString(d.Name()) {
			os.Remove(path)
		}
		return nil
	})
}

// placeFile makes dest a copy of src when it differs: the new file is staged
// beside dest, an existing dest is renamed aside (it may be running), the new
// one moved in, and the old copy deleted when it is not in use.
func placeFile(src, dest string) (bool, error) {
	if same, err := sameFile(src, dest); err != nil {
		return false, err
	} else if same {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return false, err
	}
	staged, err := stageCopy(src, filepath.Dir(dest))
	if err != nil {
		return false, err
	}
	old := ""
	if _, err := os.Lstat(dest); err == nil {
		if old, err = moveAside(dest); err != nil {
			os.Remove(staged)
			return false, err
		}
	}
	if err := os.Rename(staged, dest); err != nil {
		os.Remove(staged)
		if old != "" {
			os.Rename(old, dest)
		}
		return false, err
	}
	if old != "" {
		os.Remove(old)
	}
	return true, nil
}

// stageCopy copies src into a new temporary file in dir, with src's mode.
func stageCopy(src, dir string) (string, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".agent-pane.*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(out.Name(), info.Mode().Perm())
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// copyTree copies the files under from into to, creating folders as needed.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		dest := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, info.Mode().Perm())
	})
}

// syncTree makes to a copy of from, swapping in a complete copy, and reports
// whether it changed anything.
func syncTree(from, to string) (bool, error) {
	if same, _ := sameTree(from, to); same {
		return false, nil
	}
	if err := os.RemoveAll(to + ".new"); err != nil {
		return false, err
	}
	if err := copyTree(from, to+".new"); err != nil {
		return false, fmt.Errorf("could not copy %s: %w", from, err)
	}
	if err := os.RemoveAll(to); err != nil {
		return false, fmt.Errorf("could not replace %s: %w", to, err)
	}
	if err := os.Rename(to+".new", to); err != nil {
		return false, fmt.Errorf("could not install %s: %w", to, err)
	}
	return true, nil
}

// samePath compares two paths, ignoring case and trailing separators on
// Windows.
func samePath(goos, a, b string) bool {
	clean := func(s string) string {
		s = filepath.Clean(s)
		if goos == "windows" {
			s = strings.TrimRight(s, `\/`)
		}
		return s
	}
	if goos == "windows" {
		return strings.EqualFold(clean(a), clean(b))
	}
	return clean(a) == clean(b)
}

// splitPathList splits a PATH value with goos's separator.
func splitPathList(goos, list string) []string {
	if goos == "windows" {
		return strings.Split(list, ";")
	}
	return filepath.SplitList(list)
}

// pathListHas reports whether a Path value names dir, expanding %NAME%
// references with getenv on Windows.
func pathListHas(goos, list, dir string, getenv func(string) string) bool {
	for _, entry := range splitPathList(goos, list) {
		if entry == "" {
			continue
		}
		if goos == "windows" && getenv != nil {
			entry = expandWindowsEnv(entry, getenv)
		}
		if samePath(goos, entry, dir) {
			return true
		}
	}
	return false
}

var windowsEnvRef = regexp.MustCompile(`%([^%]+)%`)

// expandWindowsEnv expands %NAME% references, leaving unknown ones as they
// are, like ExpandEnvironmentStrings.
func expandWindowsEnv(s string, getenv func(string) string) string {
	return windowsEnvRef.ReplaceAllStringFunc(s, func(ref string) string {
		if v := getenv(strings.Trim(ref, "%")); v != "" {
			return v
		}
		return ref
	})
}
