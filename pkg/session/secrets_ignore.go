package session

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Acknowledging a finding, so the report shrinks as it is dealt with.
//
// A list that reports the same eighteen things every time is a list nobody
// reads twice, and the ones that genuinely need no action are not rare: a
// test fixture, an example from a vendor's documentation, a token that was
// revoked months ago. The answer is not to guess harder — it is to let the
// reader say "I have dealt with that" once and have it stick.
//
// Acknowledging is by FINGERPRINT, so this file never holds a secret either.

// IgnoredSecret is one acknowledged finding.
type IgnoredSecret struct {
	Fingerprint string    `json:"fingerprint"`
	Kind        string    `json:"kind,omitempty"`
	Note        string    `json:"note,omitempty"`
	When        time.Time `json:"when"`
}

// IgnoredSecrets is every acknowledged finding, by fingerprint.
type IgnoredSecrets map[string]IgnoredSecret

// IgnoredSecretsPath hangs off the home directory like the relinks file, so a
// test can redirect it.
func IgnoredSecretsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "verbose", "secrets-ignored.json"), nil
}

// LoadIgnoredSecrets reads the list. A missing file is an empty list, not an
// error — the same rule the relinks file follows, because a broken or absent
// config must never stop the report being produced.
func LoadIgnoredSecrets() (IgnoredSecrets, error) {
	path, err := IgnoredSecretsPath()
	if err != nil {
		return IgnoredSecrets{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return IgnoredSecrets{}, nil
	}
	if err != nil {
		return IgnoredSecrets{}, err
	}
	var list []IgnoredSecret
	if err := json.Unmarshal(data, &list); err != nil {
		return IgnoredSecrets{}, err
	}
	out := make(IgnoredSecrets, len(list))
	for _, ig := range list {
		out[ig.Fingerprint] = ig
	}
	return out, nil
}

// Save writes the list back, oldest first so a diff of the file reads as the
// order things were dealt with.
func (ig IgnoredSecrets) Save() error {
	path, err := IgnoredSecretsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	list := make([]IgnoredSecret, 0, len(ig))
	for _, v := range ig {
		list = append(list, v)
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].When.Equal(list[j].When) {
			return list[i].When.Before(list[j].When)
		}
		return list[i].Fingerprint < list[j].Fingerprint
	})
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// Add acknowledges a fingerprint. Re-adding one keeps the original date: when
// it was dealt with is the fact worth keeping.
func (ig IgnoredSecrets) Add(fingerprint, kind, note string, when time.Time) {
	if existing, ok := ig[fingerprint]; ok {
		if note != "" {
			existing.Note = note
		}
		ig[fingerprint] = existing
		return
	}
	ig[fingerprint] = IgnoredSecret{
		Fingerprint: fingerprint, Kind: kind, Note: note, When: when,
	}
}

// Remove un-acknowledges one, and reports whether it was there.
func (ig IgnoredSecrets) Remove(fingerprint string) bool {
	if _, ok := ig[fingerprint]; !ok {
		return false
	}
	delete(ig, fingerprint)
	return true
}

// Apply splits findings into the ones still to deal with and the count that
// has been acknowledged.
//
// It returns the count rather than hiding them silently: a report that quietly
// drops rows is one you cannot trust to be complete, which is the same reason
// `-limit` says when it stopped.
func (ig IgnoredSecrets) Apply(findings []SecretFinding) (kept []SecretFinding, ignored int) {
	for _, f := range findings {
		if _, skip := ig[f.Fingerprint]; skip {
			ignored++
			continue
		}
		kept = append(kept, f)
	}
	return kept, ignored
}
