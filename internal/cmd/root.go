package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	jsonparser "github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"

	"github.com/knadh/koanf/v2"

	"github.com/spf13/cobra"
)

// config holds the loaded configuration. loadConfig and the config.json
// watcher replace it whole, so readers go through cfg().
var (
	configMu      sync.RWMutex
	config        = koanf.New(".")
	configWatcher *file.File
	watchedPath   string
)

// cfg returns the current configuration snapshot.
func cfg() *koanf.Koanf {
	configMu.RLock()
	defer configMu.RUnlock()
	return config
}

var (
	maxBufferSizeBytes   = 512
	keepalivePingTimeout = 20 * time.Second
)

// homeDir is the user's home folder: %USERPROFILE% on Windows, $HOME
// elsewhere.
var homeDir = userHome(runtime.GOOS, os.Getenv)

var configDir = filepath.Join(homeDir, ".config", "agent-pane")
var cacheDir = userCacheDir(runtime.GOOS, os.Getenv)
var dataDir = userDataDir(runtime.GOOS, os.Getenv)
var shippedAgentsDir = filepath.Join(dataDir, "agents")
var userAgentsDir = filepath.Join(configDir, "agents")

var legacyConfigDir = filepath.Join(homeDir, ".config", "tweety")
var legacyCacheDir = filepath.Join(homeDir, ".cache", "tweety")
var legacyDataDir = filepath.Join(homeDir, ".local", "share", "tweety")

func userHome(goos string, getenv func(string) string) string {
	if goos == "windows" {
		return getenv("USERPROFILE")
	}
	return getenv("HOME")
}

// userDataDir is where the installer puts Agent Pane's files:
// %LOCALAPPDATA%\agent-pane on Windows, ~/.local/share/agent-pane elsewhere.
func userDataDir(goos string, getenv func(string) string) string {
	if goos == "windows" {
		return filepath.Join(getenv("LOCALAPPDATA"), "agent-pane")
	}
	return filepath.Join(userHome(goos, getenv), ".local", "share", "agent-pane")
}

func userCacheDir(goos string, getenv func(string) string) string {
	if goos == "windows" {
		return filepath.Join(userDataDir(goos, getenv), "cache")
	}
	return filepath.Join(userHome(goos, getenv), ".cache", "agent-pane")
}

func NewCmdRoot(version string) *cobra.Command {
	var skill bool
	cmd := &cobra.Command{
		Use:          "agent-pane",
		SilenceUsage: true,
		Short:        "Claude Code, Codex and Pi in a real terminal, in Chrome's side panel.",
		Version:      version,
		Args:         cobra.ArbitraryArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Printing the skill needs no configuration.
			if !cmd.HasParent() && skill {
				return nil
			}
			return loadConfig()
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if skill {
				return printSkill(cmd.OutOrStdout())
			}
			return cmd.Help()
		},
	}

	cmd.Flags().SetInterspersed(true)
	cmd.Flags().BoolVar(&skill, "skill", false, "print the Agent Pane skill, which tells a coding agent how to use Agent Pane")

	cmd.AddCommand(
		NewCmdServe(version),
		NewCmdSpawn(),
		NewCmdPage(),
		NewCmdStatus(),
		NewCmdLs(),
		NewCmdRun(),
		NewCmdSend(),
		NewCmdWait(),
		NewCmdRead(),
		NewCmdDoctor(version),
		NewCmdPlace(),
	)

	return cmd
}

// HostArgs is the command line to run for args. A browser starts a
// native-messaging host with the calling extension's origin as the first
// argument; on Windows the manifest names agent-pane.exe itself, with no
// wrapper to add "serve", so that call runs serve.
func HostArgs(args []string) []string {
	if len(args) > 0 && strings.HasPrefix(args[0], "chrome-extension://") {
		return []string{"serve"}
	}
	return args
}

func loadConfig() error {
	migrateLegacyDirs()

	configPath := filepath.Join(configDir, "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		configBytes, err := json.MarshalIndent(defaultConfig(), "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal default config: %w", err)
		}

		if err := os.WriteFile(configPath, configBytes, 0644); err != nil {
			return fmt.Errorf("failed to write default config: %w", err)
		}
	}

	configMu.Lock()
	defer configMu.Unlock()

	next, err := readConfig(configPath)
	if err != nil {
		return err
	}
	config = next

	if watchedPath == configPath {
		return nil
	}
	if configWatcher != nil {
		_ = configWatcher.Unwatch()
		configWatcher, watchedPath = nil, ""
	}
	f := file.Provider(configPath)
	err = f.Watch(func(event interface{}, err error) {
		configMu.Lock()
		defer configMu.Unlock()
		if configWatcher != f {
			return
		}
		if err != nil {
			// The watcher stops after any error; the next loadConfig starts
			// another.
			log.Printf("watch error: %v", err)
			configWatcher, watchedPath = nil, ""
			return
		}
		if next, err := readConfig(configPath); err == nil {
			config = next
		}
	})
	if err != nil {
		log.Printf("watch error: %v", err)
		return nil
	}
	configWatcher, watchedPath = f, configPath

	return nil
}

func defaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"command":   getDefaultShell(),
		"theme":     "Tomorrow Night",
		"themeDark": "Tomorrow Night",
		"xterm": map[string]interface{}{
			"fontSize": 13,
		},
	}
}

// readConfig builds a fresh configuration from the defaults and configPath.
func readConfig(configPath string) (*koanf.Koanf, error) {
	next := koanf.New(".")
	if err := next.Load(confmap.Provider(defaultConfig(), "."), nil); err != nil {
		return nil, fmt.Errorf("failed to load default config: %w", err)
	}
	if err := next.Load(file.Provider(configPath), jsonparser.Parser()); err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return next, nil
}

// unwatchConfig stops the config.json watcher, if one is running.
func unwatchConfig() {
	configMu.Lock()
	defer configMu.Unlock()
	if configWatcher != nil {
		_ = configWatcher.Unwatch()
	}
	configWatcher, watchedPath = nil, ""
}

// migrateLegacyDirs renames old tweety XDG dirs to agent-pane once.
func migrateLegacyDirs() {
	migrateDir(legacyConfigDir, configDir)
	migrateDir(legacyCacheDir, cacheDir)
	migrateDir(legacyDataDir, dataDir)
}

func migrateDir(from, to string) {
	if from == "" || to == "" || from == to {
		return
	}
	if _, err := os.Stat(to); err == nil {
		return
	}
	if _, err := os.Stat(from); err != nil {
		return
	}
	if err := os.Rename(from, to); err != nil {
		log.Printf("failed to migrate %s -> %s: %v", from, to, err)
	}
}

func configPath() string {
	return filepath.Join(configDir, "config.json")
}

// writeConfigMap merges values into config.json and reloads koanf.
func writeConfigMap(updates map[string]interface{}) error {
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	current := map[string]interface{}{}
	path := configPath()
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &current)
	}

	for key, value := range updates {
		if value == nil {
			delete(current, key)
			continue
		}
		current[key] = value
	}

	out, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return loadConfig()
}

func readConfigMap() (map[string]interface{}, error) {
	current := map[string]interface{}{}
	data, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]interface{}{
				"command": getDefaultShell(),
				"theme":   "Tomorrow Night",
			}, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &current); err != nil {
		return nil, err
	}
	return current, nil
}

func getDefaultShell() string {
	if runtime.GOOS == "windows" {
		return windowsDefaultShell(func(name string) (string, error) {
			if bin, ok := lookPathIn([]string{"PATH=" + loginPATH(), "PATHEXT=" + os.Getenv("PATHEXT")}, name); ok {
				return bin, nil
			}
			return "", exec.ErrNotFound
		})
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		switch runtime.GOOS {
		case "darwin":
			return "/bin/zsh"
		case "linux":
			return "/bin/bash"
		default:
			return "/bin/sh"
		}
	}
	return shell
}

// windowsDefaultShell is PowerShell 7 when it is on the registry's PATH, else
// Windows PowerShell, as herdr-core chooses.
func windowsDefaultShell(lookPath func(string) (string, error)) string {
	if path, err := lookPath("pwsh.exe"); err == nil {
		return path
	}
	return "powershell.exe"
}
