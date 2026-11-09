package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// paneRegistryPath is the host-private record of Durable (preview) pane
// tabs, ~/.local/share/agent-pane/state/panes.json.
var paneRegistryPath = filepath.Join(dataDir, "state", "panes.json")

// paneRecord is what a new host needs to take a Durable session back from
// the core: the core terminal carries only public ids (its
// agent-pane.session label), and this joins one to the session id and pane
// tab id the extension holds. Grants are not recorded: they end with the
// host that gave them.
type paneRecord struct {
	// ID is the host's session id, the one in the extension's storage.
	ID string `json:"id"`
	// Pane is the pane tab's secret id.
	Pane string `json:"pane"`
	// Session is the core terminal's agent-pane.session label.
	Session string `json:"session"`
	Agent   string `json:"agent,omitempty"`
	Label   string `json:"label,omitempty"`
	// Socket is the session's page socket, served again at the same path
	// because the program's AGENT_PANE_SOCKET names it.
	Socket string `json:"socket,omitempty"`
	// Browser is the extension's browser id that started or took back the
	// pane tab; another browser's host leaves it alone.
	Browser string `json:"browser,omitempty"`
	// Orphan is set for a core terminal adopted without a record.
	Orphan bool `json:"orphan,omitempty"`
}

// paneRegistry is panes.json, read and written whole. Only the user can open
// it.
type paneRegistry struct {
	path string
	mu   sync.Mutex
}

func newPaneRegistry(path string) *paneRegistry {
	return &paneRegistry{path: path}
}

func (r *paneRegistry) load() (map[string]paneRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked()
}

func (r *paneRegistry) loadLocked() (map[string]paneRecord, error) {
	records := map[string]paneRecord{}
	data, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	var file struct {
		Panes []paneRecord `json:"panes"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	for _, rec := range file.Panes {
		if rec.ID != "" && rec.Session != "" {
			records[rec.ID] = rec
		}
	}
	return records, nil
}

func (r *paneRegistry) saveLocked(records map[string]paneRecord) error {
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	file := struct {
		Panes []paneRecord `json:"panes"`
	}{Panes: make([]paneRecord, 0, len(records))}
	for _, rec := range records {
		file.Panes = append(file.Panes, rec)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".panes-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}

// update applies change to the records and writes them back.
func (r *paneRegistry) update(change func(map[string]paneRecord)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	records, err := r.loadLocked()
	if err != nil {
		// An unreadable file is replaced rather than blocking every
		// session after it.
		records = map[string]paneRecord{}
	}
	change(records)
	return r.saveLocked(records)
}

func (r *paneRegistry) put(rec paneRecord) error {
	return r.update(func(records map[string]paneRecord) { records[rec.ID] = rec })
}

func (r *paneRegistry) remove(id string) error {
	r.mu.Lock()
	records, err := r.loadLocked()
	r.mu.Unlock()
	if err == nil {
		if _, ok := records[id]; !ok {
			return nil
		}
	}
	return r.update(func(records map[string]paneRecord) { delete(records, id) })
}
