package session

import (
	"regexp"
	"testing"
)

// Redaction checked against the transcripts on this machine, the way the
// render smoke tests are: a fixture only contains the secrets someone thought
// to invent, and the patterns here were derived from a real corpus, so a real
// corpus is what can say whether they still hold.
//
// It counts and never prints. A test that quotes the secret it found to prove
// it found one has published it to every CI log that runs it.
func TestRedactionLeavesNoKnownTokenShapeInTheRealCorpus(t *testing.T) {
	// The shapes that are unambiguous enough to assert on. Assignments and
	// account keys are deliberately not here: "password = hunter2" is a
	// judgement, and a test of a judgement against someone else's history is
	// a test that fails for being right.
	shapes := map[string]*regexp.Regexp{
		"github token":      regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
		"github fine-grain": regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
		"jwt":               regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}`),
		"aws key id":        regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	}

	store, err := NewStore()
	if err != nil {
		t.Skip("no store:", err)
	}
	defer store.Close()
	store.SetRedaction(true)
	if err := store.Scan(); err != nil {
		t.Skip("no scan:", err)
	}
	infos := store.GetSessions()
	if len(infos) == 0 {
		t.Skip("no transcripts on this machine")
	}

	left := map[string]int{}
	texts := 0
	for _, info := range infos {
		sess := store.GetSession(info.ID)
		if sess == nil {
			continue
		}
		for _, e := range sess.Events {
			parts := []string{e.UserText, e.Text, e.Thinking, e.ToolOutput, info.Title}
			if c, ok := stringInput(e.ToolInput, "command"); ok {
				parts = append(parts, c)
			}
			if c, ok := stringInput(e.ToolInput, "content"); ok {
				parts = append(parts, c)
			}
			if e.Result != nil {
				parts = append(parts, e.Result.Stdout, e.Result.Stderr, e.Result.Raw)
			}
			for _, text := range parts {
				if text == "" {
					continue
				}
				texts++
				for name, re := range shapes {
					left[name] += len(re.FindAllString(text, -1))
				}
			}
		}
	}

	for name, n := range left {
		if n > 0 {
			t.Errorf("%d %s(s) survived redaction across %d sessions — the "+
				"pattern does not cover what is really on this machine",
				n, name, len(infos))
		}
	}
	t.Logf("checked %d pieces of text across %d sessions", texts, len(infos))
}
