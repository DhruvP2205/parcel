package transfer

import (
	"encoding/json"
	"os"
)

// Meta is the receiver-side sidecar (<name>.part.meta) that survives a
// dropped connection so a later attempt — even in a fresh process — knows
// how much of the file it can trust and resume after.
type Meta struct {
	Name            string   `json:"name"`
	Size            int64    `json:"size"`
	ChunkHashes     []string `json:"chunk_hashes"`
	HighestVerified int64    `json:"highest_verified"` // -1 means nothing verified yet
}

func loadMeta(path string) (Meta, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, false
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, false
	}
	return m, true
}

func saveMeta(path string, m Meta) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func sameHashes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
