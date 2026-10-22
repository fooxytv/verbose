package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func ignoreHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Windows resolves home from %USERPROFILE%, and os.UserHomeDir is what
	// every production path uses.
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
}

func TestIgnoredSecretsRoundTrip(t *testing.T) {
	ignoreHome(t)
	when := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

	ig, err := LoadIgnoredSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if len(ig) != 0 {
		t.Fatalf("%d entries before anything was written", len(ig))
	}

	ig.Add("abc12345", "github token", "a test fixture", when)
	ig.Add("def67890", "jwt", "", when.Add(time.Hour))
	if err := ig.Save(); err != nil {
		t.Fatal(err)
	}

	back, err := LoadIgnoredSecrets()
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 {
		t.Fatalf("%d entries after saving two", len(back))
	}
	if got := back["abc12345"].Note; got != "a test fixture" {
		t.Errorf("note = %q", got)
	}
	if !back["abc12345"].When.Equal(when) {
		t.Errorf("when = %v, want %v", back["abc12345"].When, when)
	}
}

// The file must never hold a secret either — it is config, and config gets
// committed, synced and shared.
func TestIgnoredSecretsFileHoldsOnlyFingerprints(t *testing.T) {
	ignoreHome(t)
	ig, _ := LoadIgnoredSecrets()
	ig.Add(Fingerprint("ghp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"), "github token", "", time.Now())
	if err := ig.Save(); err != nil {
		t.Fatal(err)
	}
	path, _ := IgnoredSecretsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); strings.Contains(got, "A1b2C3d4") {
		t.Errorf("the ignore file carries the value:\n%s", got)
	}
}

// Re-acknowledging keeps the original date: WHEN it was dealt with is the
// fact worth keeping, and a report that resets it every run forgets.
func TestAddingTwiceKeepsTheFirstDate(t *testing.T) {
	ignoreHome(t)
	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ig, _ := LoadIgnoredSecrets()
	ig.Add("abc12345", "jwt", "old note", first)
	ig.Add("abc12345", "jwt", "new note", first.Add(300*24*time.Hour))

	if got := ig["abc12345"].When; !got.Equal(first) {
		t.Errorf("when = %v, want the first date %v", got, first)
	}
	if got := ig["abc12345"].Note; got != "new note" {
		t.Errorf("note = %q, want the newer one", got)
	}
}

func TestRemoveReportsWhetherItWasThere(t *testing.T) {
	ig := IgnoredSecrets{}
	ig.Add("abc12345", "jwt", "", time.Now())
	if !ig.Remove("abc12345") {
		t.Error("Remove said it was not there")
	}
	if ig.Remove("abc12345") {
		t.Error("Remove said it removed it twice")
	}
	if len(ig) != 0 {
		t.Errorf("%d entries left", len(ig))
	}
}

// Apply has to COUNT what it hides. A report that quietly drops rows cannot
// be trusted to be complete.
func TestApplyCountsWhatItHides(t *testing.T) {
	ig := IgnoredSecrets{}
	ig.Add("aaaaaaaa", "jwt", "", time.Now())

	findings := []SecretFinding{
		{Fingerprint: "aaaaaaaa", Kind: "jwt"},
		{Fingerprint: "bbbbbbbb", Kind: "github token"},
		{Fingerprint: "cccccccc", Kind: "aws access key id"},
	}
	kept, hidden := ig.Apply(findings)
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1", hidden)
	}
	if len(kept) != 2 || kept[0].Fingerprint != "bbbbbbbb" {
		t.Errorf("kept = %+v", kept)
	}

	// Nothing ignored is everything kept, and nothing claimed hidden.
	kept, hidden = IgnoredSecrets{}.Apply(findings)
	if len(kept) != 3 || hidden != 0 {
		t.Errorf("kept %d, hidden %d, want 3 and 0", len(kept), hidden)
	}
}

// A broken or missing file must never stop the report being produced.
func TestABrokenIgnoreFileIsNotFatal(t *testing.T) {
	ignoreHome(t)
	path, _ := IgnoredSecretsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ig, err := LoadIgnoredSecrets()
	if err == nil {
		t.Error("a broken file should report an error")
	}
	if ig == nil {
		t.Fatal("a broken file must still return a usable list, not nil")
	}
	if _, hidden := ig.Apply([]SecretFinding{{Fingerprint: "aaaaaaaa"}}); hidden != 0 {
		t.Errorf("a broken list hid %d findings", hidden)
	}
}
