package relink

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fooxytv/verbose/pkg/session"
)

// fixture isolates the home directory, so links are written to a temp
// directory, and lays out a renamed folder and an unrelated missing one.
func fixture(t *testing.T) (here, renamed, unrelated string, infos []session.SessionInfo) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	here = filepath.Join(home, "new-name")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(here, "main.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	renamed = filepath.Join(home, "old-name")
	unrelated = filepath.Join(home, "scratch")
	infos = []session.SessionInfo{
		{CWD: unrelated, LastUpdate: time.Now()},
		{CWD: renamed, LastUpdate: time.Now().Add(-time.Hour), FilesWritten: []string{filepath.Join(renamed, "main.go")}},
	}
	return here, renamed, unrelated, infos
}

func run(t *testing.T, infos []session.SessionInfo, here, input string, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = Run(args, strings.NewReader(input), &o, &e, infos, here)
	return code, o.String(), e.String()
}

func links(t *testing.T) session.Relinks {
	t.Helper()
	l, err := session.LoadRelinks()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Enter takes the best match, and the best match is the folder whose files are
// here — not the more recent one.
func TestRelinkEnterTakesBestMatch(t *testing.T) {
	here, renamed, _, infos := fixture(t)
	code, out, errOut := run(t, infos, here, "\n")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(out, "1 of 1 files found here") {
		t.Errorf("list should show the evidence:\n%s", out)
	}
	l := links(t)
	if len(l) != 1 || l[0].From != renamed || l[0].To != here {
		t.Errorf("links = %+v, want %s → %s", l, renamed, here)
	}
}

func TestRelinkPicksByNumberAndCancels(t *testing.T) {
	here, _, unrelated, infos := fixture(t)
	if code, _, _ := run(t, infos, here, "2\n"); code != 0 {
		t.Fatal("picking 2 failed")
	}
	if l := links(t); len(l) != 1 || l[0].From != unrelated {
		t.Errorf("links = %+v, want the second folder", l)
	}

	here, _, _, infos = fixture(t)
	if code, out, _ := run(t, infos, here, "nope\n"); code == 0 || !strings.Contains(out, "Nothing changed") {
		t.Errorf("a bad answer should change nothing: exit %d\n%s", code, out)
	}
	if l := links(t); len(l) != 0 {
		t.Errorf("cancelled run wrote %+v", l)
	}
}

// Enter is not a yes when nothing here backs up the guess.
func TestRelinkNoDefaultWithoutEvidence(t *testing.T) {
	here, _, unrelated, _ := fixture(t)
	infos := []session.SessionInfo{{CWD: unrelated}, {CWD: unrelated + "2"}}
	if code, _, _ := run(t, infos, here, "\n"); code == 0 {
		t.Error("Enter with no evidence should not link anything")
	}
	if code, _, _ := run(t, infos, here, "", "-y"); code == 0 {
		t.Error("-y with no evidence should refuse")
	}
	if l := links(t); len(l) != 0 {
		t.Errorf("links = %+v, want none", l)
	}

	// One file in common out of several is a coincidence, not a rename: the
	// real case was a shared README making an unrelated folder the default.
	weak := []session.SessionInfo{{CWD: unrelated}, {CWD: unrelated + "2", FilesRead: []string{
		filepath.Join(unrelated+"2", "main.go"), filepath.Join(unrelated+"2", "a.go"), filepath.Join(unrelated+"2", "b.go"),
	}}}
	if code, _, _ := run(t, weak, here, "\n"); code == 0 {
		t.Error("Enter on 1 of 3 files found should not link anything")
	}
	if l := links(t); len(l) != 0 {
		t.Errorf("links = %+v, want none", l)
	}
}

func TestRelinkByNameListAndRemove(t *testing.T) {
	here, renamed, _, infos := fixture(t)
	code, _, errOut := run(t, infos, here, "y\n", "OLD-NAME")
	if code != 0 {
		t.Fatalf("by name: exit %d: %s", code, errOut)
	}
	if code, _, _ := run(t, infos, here, "", "no-such-folder"); code == 0 {
		t.Error("an unmatched name should fail")
	}

	_, out, _ := run(t, infos, here, "", "-list")
	if !strings.Contains(out, renamed+" → "+here) {
		t.Errorf("-list = %q", out)
	}
	if code, _, _ := run(t, infos, here, "", "-remove", renamed); code != 0 {
		t.Error("-remove failed")
	}
	if l := links(t); len(l) != 0 {
		t.Errorf("after -remove: %+v", l)
	}
	if code, _, _ := run(t, infos, here, "", "-remove", renamed); code == 0 {
		t.Error("removing a link that is not there should fail")
	}
}
