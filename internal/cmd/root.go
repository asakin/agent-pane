package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
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

var configDir = filepath.Join(os.Getenv("HOME"), ".config", "agent-pane")
var cacheDir = filepath.Join(os.Getenv("HOME"), ".cache", "agent-pane")
var dataDir = filepath.Join(os.Getenv("HOME"), ".local", "share", "agent-pane")
var commandDir = filepath.Join(configDir, "commands")
var shippedAgentsDir = filepath.Join(dataDir, "agents")
var userAgentsDir = filepath.Join(configDir, "agents")

var legacyConfigDir = filepath.Join(os.Getenv("HOME"), ".config", "tweety")
var legacyCacheDir = filepath.Join(os.Getenv("HOME"), ".cache", "tweety")
var legacyDataDir = filepath.Join(os.Getenv("HOME"), ".local", "share", "tweety")

func NewCmdRoot(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent-pane",
		SilenceUsage: true,
		Short:        "Claude Code, Codex and Pi in a real terminal, in Chrome's side panel.",
		Version:      version,
		Args:         cobra.ArbitraryArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return loadConfig()
		},
	}

	cmd.Flags().SetInterspersed(true)

	cmd.AddCommand(
		NewCmdServe(version),
		NewCmdRun(),
		NewCmdSpawn(),
		NewCmdPage(),
		NewCmdDoctor(version),
	)

	return cmd
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
