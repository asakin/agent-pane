package herdrcore

import (
	"context"
	"encoding/json"
)

// Doc is a document the core keeps by name: an opaque JSON value and its
// version, zero for a name never written. A deleted one has a null value
// and keeps its version.
type Doc struct {
	Value   json.RawMessage `json:"value"`
	Version uint64          `json:"version"`
}

// DocGet reads the document name.
func (c *Client) DocGet(ctx context.Context, name string) (Doc, error) {
	var d Doc
	err := c.Call(ctx, "doc.get", map[string]any{"name": name}, &d)
	return d, err
}

// DocSet writes value as the document name if its version is still version,
// and returns the new version. A writer that lost the race gets an error of
// kind "conflict" (see IsKind) and should read the document again. The core
// sends a "doc" event to every subscriber.
func (c *Client) DocSet(ctx context.Context, name string, value any, version uint64) (uint64, error) {
	var r struct {
		Version uint64 `json:"version"`
	}
	err := c.Call(ctx, "doc.set", map[string]any{"name": name, "value": value, "version": version}, &r)
	return r.Version, err
}

// DocName is the document a "doc" event says changed.
func (e Event) DocName() (string, bool) {
	if e.Type != "doc" {
		return "", false
	}
	var data struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(e.Data, &data) != nil || data.Name == "" {
		return "", false
	}
	return data.Name, true
}
