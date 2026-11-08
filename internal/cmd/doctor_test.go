package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// healthyInstall writes, by hand, what scripts/place.sh leaves behind for a
// release of the given extension version.
func healthyInstall(t *testing.T, goos, extensionVersion string) installLayout {
	t.Helper()
	l := layoutFor(t.TempDir())
	release := map[string]string{
		"agent-pane":                      "binary",
		"instructions.md":                 "<!-- agent-pane:start -->\n# Agent Pane\n<!-- agent-pane:end -->\n",
		"place.sh":                        "#!/bin/sh\n",
		"agents/enabled.json":             `{"agents":{}}`,
		"skills/agent-pane/SKILL.md":      "name: agent-pane\n",
		"extensions/chrome/manifest.json": `{"version":"` + extensionVersion + `"}`,
		"extensions/chrome/background.js": "//\n",
	}
	for rel, content := range release {
		writeFile(t, filepath.Join(l.release, rel), content, 0o644)
	}
	writeFile(t, l.binary, "binary", 0o755)
	writeFile(t, l.host, "#!/bin/sh\nexec "+shQuote(l.binary)+" serve\n", 0o755)
	browser := browserDirs(goos, l.home)[0]
	manifest, _ := json.Marshal(map[string]any{
		"name": nativeHostName, "type": "stdio", "path": l.host,
		"allowed_origins": []string{extensionOrigin},
	})
	writeFile(t, filepath.Join(browser.hosts, nativeHostName+".json"), string(manifest), 0o644)
	if goos == "darwin" {
		writeFile(t, filepath.Join(l.home, "Library", "LaunchAgents", spawnAgentLabel+".plist"),
			spawnAgentPlist(l.binary), 0o644)
	}
	writeFile(t, filepath.Join(l.home, ".zshrc"), "# agent-pane:path\n", 0o644)
	writeFile(t, filepath.Join(l.configDir, "AGENTS.md"), release["instructions.md"], 0o644)
	writeFile(t, filepath.Join(l.dataDir, "agents", "enabled.json"), release["agents/enabled.json"], 0o644)
	writeFile(t, filepath.Join(l.dataDir, "skills", "agent-pane", "SKILL.md"), release["skills/agent-pane/SKILL.md"], 0o644)
	for _, dir := range []string{".agents", ".claude"} {
		copyDir := filepath.Join(l.home, dir, "skills", "agent-pane")
		writeFile(t, filepath.Join(copyDir, "SKILL.md"), release["skills/agent-pane/SKILL.md"], 0o644)
		writeFile(t, filepath.Join(copyDir, skillMarker), "ours", 0o644)
	}
	for _, name := range []string{"manifest.json", "background.js"} {
		writeFile(t, filepath.Join(l.dataDir, "extensions", "chrome", name), release["extensions/chrome/"+name], 0o644)
	}
	return l
}

// statuses gives each item its worst result.
func statuses(results []checkResult) map[string]checkStatus {
	rank := map[checkStatus]int{"": 0, checkOK: 1, checkWarn: 2, checkFail: 3}
	worst := map[string]checkStatus{}
	for _, r := range results {
		if rank[r.status] > rank[worst[r.item]] {
			worst[r.item] = r.status
		}
	}
	return worst
}

func TestDoctorHealthyInstall(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			l := healthyInstall(t, goos, "0.3.0")
			env := doctorEnv{goos: goos, version: "0.3.0", spawnAgent: func() bool { return true }}
			for _, r := range runChecks(l, env) {
				if r.status != checkOK {
					t.Errorf("%s %s: %s", r.status, r.item, r.detail)
				}
			}
		})
	}
}

func TestDoctorFindsProblems(t *testing.T) {
	cases := []struct {
		name   string
		item   string
		want   checkStatus
		goos   string
		env    func(t *testing.T, e *doctorEnv)
		damage func(t *testing.T, l installLayout)
	}{
		{name: "binary differs", item: "binary", want: checkFail, damage: func(t *testing.T, l installLayout) {
			writeFile(t, l.binary, "old binary", 0o755)
		}},
		{name: "binary missing", item: "binary", want: checkFail, damage: func(t *testing.T, l installLayout) {
			os.Remove(l.binary)
		}},
		{name: "binary not executable", item: "binary", want: checkFail, damage: func(t *testing.T, l installLayout) {
			os.Chmod(l.binary, 0o644)
		}},
		{name: "plist without the spawn argument", item: "spawn agent", want: checkFail, goos: "darwin", damage: func(t *testing.T, l installLayout) {
			plist := filepath.Join(l.home, "Library", "LaunchAgents", spawnAgentLabel+".plist")
			writeFile(t, plist, "<string>"+xmlEscape(l.binary)+"</string>", 0o644)
		}},
		{name: "spawn agent restart pending", item: "spawn agent", want: checkFail, goos: "darwin", damage: func(t *testing.T, l installLayout) {
			writeFile(t, filepath.Join(l.dataDir, "spawn-restart-pending"), "", 0o644)
		}},
		{name: "instructions emptied", item: "instructions", want: checkFail, damage: func(t *testing.T, l installLayout) {
			writeFile(t, filepath.Join(l.configDir, "AGENTS.md"), "", 0o644)
		}},
		{name: "instructions block without content", item: "instructions", want: checkFail, damage: func(t *testing.T, l installLayout) {
			writeFile(t, filepath.Join(l.configDir, "AGENTS.md"), "<!-- agent-pane:start -->\n<!-- agent-pane:end -->\n", 0o644)
		}},
		{name: "host runs another binary", item: "native host", want: checkFail, damage: func(t *testing.T, l installLayout) {
			writeFile(t, l.host, "#!/bin/sh\nexec /elsewhere serve\n", 0o755)
		}},
		{name: "manifest points elsewhere", item: "manifest", want: checkFail, damage: func(t *testing.T, l installLayout) {
			b := browserDirs("linux", l.home)[0]
			writeFile(t, filepath.Join(b.hosts, nativeHostName+".json"), `{"name":"com.agentpane.native","type":"stdio","path":"/elsewhere","allowed_origins":["`+extensionOrigin+`"]}`, 0o644)
		}},
		{name: "browser without manifest", item: "manifest", want: checkFail, damage: func(t *testing.T, l installLayout) {
			os.MkdirAll(browserDirs("linux", l.home)[1].parent, 0o755)
		}},
		{name: "no browser", item: "manifest", want: checkFail, damage: func(t *testing.T, l installLayout) {
			os.RemoveAll(filepath.Join(l.home, ".config", "google-chrome"))
		}},
		{name: "spawn agent not loaded", item: "spawn agent", want: checkFail, goos: "darwin",
			env: func(_ *testing.T, e *doctorEnv) { e.spawnAgent = func() bool { return false } }},
		{name: "PATH not set up", item: "PATH", want: checkFail, damage: func(t *testing.T, l installLayout) {
			os.Remove(filepath.Join(l.home, ".zshrc"))
		}},
		{name: "another agent-pane first on PATH", item: "PATH", want: checkWarn, env: func(t *testing.T, e *doctorEnv) {
			other := t.TempDir()
			writeFile(t, filepath.Join(other, "agent-pane"), "x", 0o755)
			e.path = other
		}},
		{name: "agent catalog stale", item: "agent catalog", want: checkFail, damage: func(t *testing.T, l installLayout) {
			writeFile(t, filepath.Join(l.dataDir, "agents", "extra.json"), "{}", 0o644)
		}},
		{name: "skill copy stale", item: "skill copy", want: checkFail, damage: func(t *testing.T, l installLayout) {
			writeFile(t, filepath.Join(l.home, ".claude", "skills", "agent-pane", "SKILL.md"), "stale", 0o644)
		}},
		{name: "skill copy missing", item: "skill copy", want: checkFail, damage: func(t *testing.T, l installLayout) {
			os.RemoveAll(filepath.Join(l.home, ".claude", "skills", "agent-pane"))
		}},
		{name: "skill folder not ours", item: "skill copy", want: checkWarn, damage: func(t *testing.T, l installLayout) {
			os.Remove(filepath.Join(l.home, ".claude", "skills", "agent-pane", skillMarker))
		}},
		{name: "extension stale", item: "extension", want: checkFail, damage: func(t *testing.T, l installLayout) {
			writeFile(t, filepath.Join(l.dataDir, "extensions", "chrome", "background.js"), "old", 0o644)
		}},
		{name: "versions differ", item: "versions", want: checkFail, env: func(_ *testing.T, e *doctorEnv) { e.version = "0.4.0" }},
		{name: "dev binary skips the version check", item: "versions", want: "", env: func(_ *testing.T, e *doctorEnv) { e.version = "dev" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			goos := tc.goos
			if goos == "" {
				goos = "linux"
			}
			l := healthyInstall(t, goos, "0.3.0")
			env := doctorEnv{goos: goos, version: "0.3.0", spawnAgent: func() bool { return true }}
			if tc.env != nil {
				tc.env(t, &env)
			}
			if tc.damage != nil {
				tc.damage(t, l)
			}
			if got := statuses(runChecks(l, env))[tc.item]; got != tc.want {
				t.Fatalf("%s: got %q, want %q", tc.item, got, tc.want)
			}
		})
	}
}

func TestDoctorWithoutRelease(t *testing.T) {
	l := healthyInstall(t, "linux", "0.3.0")
	os.RemoveAll(l.release)
	results := runChecks(l, doctorEnv{goos: "linux", version: "0.3.0"})
	if len(results) != 1 || results[0].status != checkFail || results[0].item != "release" {
		t.Fatalf("got %+v, want one failed release check", results)
	}
	if err := runPlace(l, nil, nil); err == nil {
		t.Fatal("runPlace succeeded with no release")
	}
}
