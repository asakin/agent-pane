package cmd

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

// The installed layout, as scripts/place.sh writes it. place.sh is the only
// writer; doctor reads the same paths and compares them with the copy of the
// release kept in releaseDir.
type installLayout struct {
	home      string
	binary    string // ~/.local/bin/agent-pane
	dataDir   string // ~/.local/share/agent-pane
	release   string // dataDir/release, the release place.sh last installed
	host      string // dataDir/native_messaging_host
	configDir string // ~/.config/agent-pane
	core      string // dataDir/libexec/hc, the bundled herdr-core binary
}

const (
	nativeHostName    = "com.agentpane.native"
	extensionOrigin   = "chrome-extension://eakooboihfgnikdhdldcmoiafgioeglm/"
	spawnAgentLabel   = "com.agentpane.spawn"
	skillMarker       = ".agent-pane-managed"
	installCommandURL = "https://github.com/asakin/agent-pane/releases/latest/download/install.sh"
)

func layoutFor(home string) installLayout {
	data := filepath.Join(home, ".local", "share", "agent-pane")
	return installLayout{
		home:      home,
		binary:    filepath.Join(home, ".local", "bin", "agent-pane"),
		dataDir:   data,
		release:   filepath.Join(data, "release"),
		host:      filepath.Join(data, "native_messaging_host"),
		configDir: filepath.Join(home, ".config", "agent-pane"),
		core:      filepath.Join(data, "libexec", "hc"),
	}
}

type browserDir struct {
	name   string
	parent string // the browser's own config folder; no manifest when it is missing
	hosts  string // where its native-messaging manifests go
}

// browserDirs is the list in scripts/place.sh write_manifests; change both.
func browserDirs(goos, home string) []browserDir {
	switch goos {
	case "darwin":
		s := filepath.Join(home, "Library", "Application Support")
		return []browserDir{
			{"Google Chrome", filepath.Join(s, "Google", "Chrome"), filepath.Join(s, "Google", "Chrome", "NativeMessagingHosts")},
			{"Chromium", filepath.Join(s, "Chromium"), filepath.Join(s, "Chromium", "NativeMessagingHosts")},
			{"Brave", filepath.Join(s, "BraveSoftware", "Brave-Browser"), filepath.Join(s, "BraveSoftware", "Brave-Browser", "NativeMessagingHosts")},
			{"Vivaldi", filepath.Join(s, "Vivaldi"), filepath.Join(s, "Vivaldi", "NativeMessagingHosts")},
			{"Helium", filepath.Join(s, "net.imput.helium"), filepath.Join(s, "net.imput.helium", "NativeMessagingHosts")},
			{"Microsoft Edge", filepath.Join(s, "Microsoft", "Edge"), filepath.Join(s, "Microsoft", "Edge", "NativeMessagingHosts")},
		}
	case "linux":
		c := filepath.Join(home, ".config")
		return []browserDir{
			{"Google Chrome", filepath.Join(c, "google-chrome"), filepath.Join(c, "google-chrome", "native-messaging-hosts")},
			{"Chromium", filepath.Join(c, "chromium"), filepath.Join(c, "chromium", "native-messaging-hosts")},
			{"Microsoft Edge", filepath.Join(c, "microsoft-edge"), filepath.Join(c, "microsoft-edge", "native-messaging-hosts")},
			{"Brave", filepath.Join(c, "brave"), filepath.Join(c, "brave", "native-messaging-hosts")},
			{"Vivaldi", filepath.Join(c, "vivaldi"), filepath.Join(c, "vivaldi", "native-messaging-hosts")},
			{"Helium", filepath.Join(c, "helium"), filepath.Join(c, "helium", "native-messaging-hosts")},
		}
	}
	return nil
}

type checkStatus string

const (
	checkOK   checkStatus = "ok"
	checkWarn checkStatus = "warn"
	checkFail checkStatus = "FAIL"
)

type checkResult struct {
	status checkStatus
	item   string
	detail string
}

// doctorEnv is what the checks read from outside the file system.
type doctorEnv struct {
	goos    string
	goarch  string
	version string // this binary's version
	path    string // $PATH
	// spawnAgent reports whether the macOS spawn agent is loaded; nil skips
	// the check.
	spawnAgent func() bool
	// coreVersion runs the bundled core's `--version`; nil skips the check.
	coreVersion func(path string) (string, error)
}

func NewCmdDoctor(version string) *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check that Agent Pane is installed correctly, and repair it with --fix",
		Long: `Checks the files the installer owns against the release it installed,
kept in ~/.local/share/agent-pane/release, and reports each one. ~/AGENTS.md
and ~/CLAUDE.md are yours and are not checked.

--fix runs that release's place.sh again when something is wrong. It uses
only files already on this computer and does not change the installed version;
to update, run the install command again.`,
		Args: cobra.NoArgs,
		// doctor reads the installation; it does not need, or create, config.json.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			layout := layoutFor(os.Getenv("HOME"))
			env := doctorEnv{
				goos:        runtime.GOOS,
				goarch:      runtime.GOARCH,
				version:     version,
				path:        os.Getenv("PATH"),
				spawnAgent:  spawnAgentLoaded,
				coreVersion: coreVersion,
			}
			out := cmd.OutOrStdout()
			problems := printChecks(out, runChecks(layout, env))
			if problems == 0 {
				if fix {
					fmt.Fprintln(out, "agent-pane doctor: nothing to fix")
				}
				return nil
			}
			if !fix {
				return fmt.Errorf("%d problem(s); run `agent-pane doctor --fix` to put the installed release back in place", problems)
			}
			return runPlace(layout, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "put the installed release back in place when a check fails")
	return cmd
}

func printChecks(w io.Writer, results []checkResult) int {
	problems := 0
	for _, r := range results {
		fmt.Fprintf(w, "%-4s  %-18s %s\n", r.status, r.item, r.detail)
		if r.status == checkFail {
			problems++
		}
	}
	return problems
}

func runPlace(l installLayout, stdout, stderr io.Writer) error {
	place := filepath.Join(l.release, "place.sh")
	if _, err := os.Stat(place); err != nil {
		return fmt.Errorf("no installed release at %s to repair from; install again with `curl -fsSL %s | sh` (or scripts/dev-install.sh in a checkout)", l.release, installCommandURL)
	}
	c := exec.Command("sh", place)
	c.Stdout, c.Stderr = stdout, stderr
	return c.Run()
}

func spawnAgentLoaded() bool {
	if os.Getenv("AGENT_PANE_SKIP_LAUNCHD") != "" {
		return true
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), spawnAgentLabel)
	return exec.Command("launchctl", "print", target).Run() == nil
}

func coreVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	return strings.TrimSpace(string(out)), err
}

func runChecks(l installLayout, env doctorEnv) []checkResult {
	var out []checkResult
	add := func(status checkStatus, item, format string, args ...any) {
		detail := strings.ReplaceAll(fmt.Sprintf(format, args...), l.home+string(filepath.Separator), "~/")
		out = append(out, checkResult{status, item, detail})
	}

	for _, name := range []string{"agent-pane", "place.sh"} {
		if _, err := os.Stat(filepath.Join(l.release, name)); err != nil {
			add(checkFail, "release", "%s has no %s; install again with `curl -fsSL %s | sh`", l.release, name, installCommandURL)
			return out
		}
	}
	add(checkOK, "release", "%s", l.release)

	if same, err := sameFile(filepath.Join(l.release, "agent-pane"), l.binary); err != nil {
		add(checkFail, "binary", "%s: %v", l.binary, err)
	} else if !same {
		add(checkFail, "binary", "%s differs from the installed release", l.binary)
	} else if info, err := os.Stat(l.binary); err != nil || info.Mode()&0o111 == 0 {
		add(checkFail, "binary", "%s is not executable", l.binary)
	} else {
		add(checkOK, "binary", "%s (%s)", l.binary, env.version)
	}

	wantHost := "#!/bin/sh\nexec " + shQuote(l.binary) + " serve\n"
	if info, err := os.Stat(l.host); err != nil {
		add(checkFail, "native host", "%s is missing", l.host)
	} else if got, _ := os.ReadFile(l.host); string(got) != wantHost || info.Mode()&0o111 == 0 {
		add(checkFail, "native host", "%s does not run %s", l.host, l.binary)
	} else {
		add(checkOK, "native host", "%s", l.host)
	}

	browsers := 0
	for _, b := range browserDirs(env.goos, l.home) {
		if _, err := os.Stat(b.parent); err != nil {
			continue
		}
		browsers++
		path := filepath.Join(b.hosts, nativeHostName+".json")
		if problem := checkManifest(path, l.host); problem != "" {
			add(checkFail, "manifest", "%s: %s", b.name, problem)
		} else {
			add(checkOK, "manifest", "%s", b.name)
		}
	}
	if browsers == 0 {
		add(checkFail, "manifest", "no Chromium browser config folder found")
	}

	if env.goos == "darwin" {
		plist := filepath.Join(l.home, "Library", "LaunchAgents", spawnAgentLabel+".plist")
		got, err := os.ReadFile(plist)
		_, pendingErr := os.Stat(filepath.Join(l.dataDir, "spawn-restart-pending"))
		switch {
		case err != nil:
			add(checkFail, "spawn agent", "%s is missing", plist)
		case string(got) != spawnAgentPlist(l.binary):
			add(checkFail, "spawn agent", "%s is not the plist that starts `%s spawn`", plist, l.binary)
		case env.spawnAgent != nil && !env.spawnAgent():
			add(checkFail, "spawn agent", "%s is not loaded", spawnAgentLabel)
		case pendingErr == nil:
			add(checkFail, "spawn agent", "%s has not been restarted since the binary or plist changed", spawnAgentLabel)
		default:
			add(checkOK, "spawn agent", "%s", spawnAgentLabel)
		}
	}

	for _, r := range checkCore(l, env) {
		add(r.status, r.item, "%s", r.detail)
	}

	path := checkPath(l, env.path)
	add(path.status, path.item, "%s", path.detail)

	// ~/AGENTS.md and ~/CLAUDE.md are the person's files: place.sh only
	// updates a block they keep, so they are not checked.
	instructions := filepath.Join(l.configDir, "AGENTS.md")
	if same, err := sameFile(filepath.Join(l.release, "instructions.md"), instructions); err != nil {
		add(checkFail, "instructions", "%s: %v", instructions, err)
	} else if !same {
		add(checkFail, "instructions", "%s differs from the installed release", instructions)
	} else {
		add(checkOK, "instructions", "%s", instructions)
	}

	compareTree := func(item, from, to string) {
		if same, err := sameTree(from, to); err != nil {
			add(checkFail, item, "%s: %v", to, err)
		} else if !same {
			add(checkFail, item, "%s differs from the installed release", to)
		} else {
			add(checkOK, item, "%s", to)
		}
	}
	compareTree("agent catalog", filepath.Join(l.release, "agents"), filepath.Join(l.dataDir, "agents"))
	releaseSkill := filepath.Join(l.release, "skills", "agent-pane")
	compareTree("skill", releaseSkill, filepath.Join(l.dataDir, "skills", "agent-pane"))
	for _, dir := range []string{
		filepath.Join(l.home, ".agents", "skills", "agent-pane"),
		filepath.Join(l.home, ".claude", "skills", "agent-pane"),
	} {
		info, err := os.Lstat(dir)
		if err != nil {
			add(checkFail, "skill copy", "%s is missing", dir)
			continue
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			add(checkWarn, "skill copy", "%s is a symlink, not Agent Pane's; left alone", dir)
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, skillMarker)); err != nil {
			add(checkWarn, "skill copy", "%s is not Agent Pane's; left alone", dir)
			continue
		}
		compareTree("skill copy", releaseSkill, dir)
	}

	installedExtension := filepath.Join(l.dataDir, "extensions", "chrome")
	compareTree("extension", filepath.Join(l.release, "extensions", "chrome"), installedExtension)
	if env.version != "dev" {
		if v, err := manifestVersion(filepath.Join(installedExtension, "manifest.json")); err == nil && v != strings.TrimPrefix(env.version, "v") {
			add(checkFail, "versions", "the extension is %s and the binary is %s; install a release again with the install command", v, env.version)
		}
	}
	return out
}

// coreLock is scripts/herdr-core.lock, as the release carries it.
type coreLock struct {
	version string
	// binaries maps <goos>_<goarch> to the SHA-256 of its herdr-core binary.
	binaries map[string]string
}

func readCoreLock(path string) (coreLock, error) {
	f, err := os.Open(path)
	if err != nil {
		return coreLock{}, err
	}
	defer f.Close()
	lock := coreLock{binaries: map[string]string{}}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		switch {
		case len(fields) == 0 || strings.HasPrefix(fields[0], "#"):
		case fields[0] == "version" && len(fields) == 2:
			lock.version = fields[1]
		case fields[0] == "generation" && len(fields) == 2:
		case len(fields) == 4:
			lock.binaries[fields[0]] = fields[3]
		default:
			return coreLock{}, fmt.Errorf("unexpected line %q", scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		return coreLock{}, err
	}
	if lock.version == "" {
		return coreLock{}, fmt.Errorf("no version line")
	}
	return lock, nil
}

// checkCore checks the bundled herdr-core binary against the lock the
// release carries, its license files, and that a person's own `hc` neither is
// it nor reaches it through PATH.
func checkCore(l installLayout, env doctorEnv) []checkResult {
	var out []checkResult
	add := func(status checkStatus, item, detail string) {
		out = append(out, checkResult{status, item, detail})
	}

	lockPath := filepath.Join(l.release, "herdr-core.lock")
	lock, err := readCoreLock(lockPath)
	platform := env.goos + "_" + env.goarch
	want, ok := lock.binaries[platform]
	switch {
	case err != nil:
		add(checkFail, "core", fmt.Sprintf("%s: %v", lockPath, err))
	case !ok:
		add(checkFail, "core", fmt.Sprintf("%s has no herdr-core build for %s", lockPath, platform))
	default:
		data, err := os.ReadFile(l.core)
		info, statErr := os.Stat(l.core)
		sum := sha256.Sum256(data)
		switch {
		case err != nil || statErr != nil:
			add(checkFail, "core", l.core+" is missing")
		case hex.EncodeToString(sum[:]) != want:
			add(checkFail, "core", fmt.Sprintf("%s is not the pinned herdr-core %s for %s", l.core, lock.version, platform))
		case info.Mode()&0o111 == 0:
			add(checkFail, "core", l.core+" is not executable")
		default:
			version := ""
			if env.coreVersion != nil {
				version, err = env.coreVersion(l.core)
			}
			switch {
			case err != nil:
				add(checkFail, "core", fmt.Sprintf("%s --version: %v", l.core, err))
			case env.coreVersion != nil && version != "hc "+lock.version:
				add(checkFail, "core", fmt.Sprintf("%s reports %q, want hc %s", l.core, version, lock.version))
			default:
				add(checkOK, "core", fmt.Sprintf("%s (herdr-core %s)", l.core, lock.version))
			}
		}
	}

	licenses := filepath.Join(l.dataDir, "licenses", "herdr-core")
	if same, err := sameTree(filepath.Join(l.release, "licenses", "herdr-core"), licenses); err != nil {
		add(checkFail, "core licenses", fmt.Sprintf("%s: %v", licenses, err))
	} else if !same {
		add(checkFail, "core licenses", licenses+" differs from the installed release")
	} else if missing := missingCoreLicense(licenses); missing != "" {
		add(checkFail, "core licenses", licenses+" has no "+missing)
	} else {
		add(checkOK, "core licenses", licenses)
	}

	// A person's own hc, wherever it is, is theirs and fine. Agent Pane's core,
	// installed or the copy the kept release holds, must not be reachable as
	// `hc` on PATH, ~/.local/bin/hc included.
	bundled := []string{l.core, filepath.Join(l.release, "libexec", "hc")}
	other := ""
	for _, dir := range append(filepath.SplitList(env.path), filepath.Dir(l.binary)) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, "hc")
		isBundled := false
		for _, b := range bundled {
			if filepath.Clean(dir) == filepath.Dir(b) || sameInode(candidate, b) {
				isBundled = true
			}
		}
		if isBundled {
			add(checkFail, "core isolation", candidate+" is Agent Pane's bundled core; only Agent Pane runs it, so take it off PATH")
			return out
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && other == "" {
			other = candidate
		}
	}
	if other != "" {
		add(checkOK, "core isolation", "the bundled core is not on PATH; "+other+" is a separate hc, left alone")
	} else {
		add(checkOK, "core isolation", "the bundled core is not on PATH")
	}
	return out
}

// missingCoreLicense names the first file herdr-core's license requires that
// dir lacks, or returns "".
func missingCoreLicense(dir string) string {
	for _, name := range []string{"LICENSE", "THIRD-PARTY-NOTICES"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Size() == 0 {
			return name
		}
	}
	return ""
}

// sameInode reports whether a and b, after symlinks, are the same file.
func sameInode(a, b string) bool {
	x, err := os.Stat(a)
	if err != nil {
		return false
	}
	y, err := os.Stat(b)
	return err == nil && os.SameFile(x, y)
}

// spawnAgentPlist is the LaunchAgent plist scripts/place.sh write_launchd
// writes; change both.
func spawnAgentPlist(binary string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + spawnAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(binary) + `</string>
		<string>spawn</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`
}

func checkManifest(path, host string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return path + " is missing"
	}
	var m struct {
		Name           string   `json:"name"`
		Path           string   `json:"path"`
		Type           string   `json:"type"`
		AllowedOrigins []string `json:"allowed_origins"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return path + " is not valid JSON"
	}
	if m.Name != nativeHostName || m.Type != "stdio" || m.Path != host || !slices.Equal(m.AllowedOrigins, []string{extensionOrigin}) {
		return path + " does not point the extension at " + host
	}
	return ""
}

// checkPath passes when ~/.local/bin is on PATH, or when the shell rc file
// place.sh edits will put it there in a new terminal.
func checkPath(l installLayout, pathEnv string) checkResult {
	binDir := filepath.Dir(l.binary)
	for _, dir := range filepath.SplitList(pathEnv) {
		candidate := filepath.Join(dir, "agent-pane")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			if filepath.Clean(dir) == binDir {
				return checkResult{checkOK, "PATH", binDir}
			}
			return checkResult{checkWarn, "PATH", candidate + " comes before " + l.binary + " on PATH"}
		}
	}
	for _, rc := range []string{".zshrc", ".bashrc"} {
		if data, err := os.ReadFile(filepath.Join(l.home, rc)); err == nil && bytes.Contains(data, []byte("# agent-pane:path")) {
			return checkResult{checkOK, "PATH", binDir + " (added in ~/" + rc + "; open a new terminal)"}
		}
	}
	return checkResult{checkFail, "PATH", binDir + " is not on PATH"}
}

func manifestVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", err
	}
	return m.Version, nil
}

func sameFile(a, b string) (bool, error) {
	x, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	y, err := os.ReadFile(b)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(x, y), nil
}

// sameTree reports whether two folders hold the same files with the same
// content, ignoring the skill ownership marker, like place.sh's same_tree.
func sameTree(a, b string) (bool, error) {
	fa, err := treeFiles(a)
	if err != nil {
		return false, err
	}
	fb, err := treeFiles(b)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(fa) != len(fb) {
		return false, nil
	}
	for rel, data := range fa {
		other, ok := fb[rel]
		if !ok || !bytes.Equal(data, other) {
			return false, nil
		}
	}
	return true, nil
}

func treeFiles(root string) (map[string][]byte, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() == skillMarker {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[rel] = data
		return nil
	})
	return files, err
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
