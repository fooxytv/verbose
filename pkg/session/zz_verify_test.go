package session

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestProbeRedactionOnRealCorpus(t *testing.T) {
	leftovers := map[string]*regexp.Regexp{
		"github-token":     regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
		"azure-accountkey": regexp.MustCompile(`(?i)AccountKey=[A-Za-z0-9+/=]{16,}`),
		"jwt":              regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}`),
	}

	for _, on := range []bool{false, true} {
		store, err := NewStore()
		if err != nil {
			t.Skip(err)
		}
		store.SetRedaction(on)
		start := time.Now()
		if err := store.Scan(); err != nil {
			t.Skip(err)
		}
		elapsed := time.Since(start)

		found := map[string]int{}
		marks, chars, taskWords := 0, 0, 0
		for _, in := range store.GetSessions() {
			s := store.GetSession(in.ID)
			if s == nil {
				continue
			}
			for _, e := range s.Events {
				texts := []string{e.UserText, e.Text, e.ToolOutput, e.Thinking, in.Title}
				if c, ok := stringInput(e.ToolInput, "command"); ok {
					texts = append(texts, c)
				}
				if c, ok := stringInput(e.ToolInput, "content"); ok {
					texts = append(texts, c)
				}
				if e.Result != nil {
					texts = append(texts, e.Result.Stdout, e.Result.Stderr, e.Result.Raw)
				}
				for _, txt := range texts {
					chars += len(txt)
					marks += strings.Count(txt, RedactedMark)
					taskWords += strings.Count(txt, "task-")
					for name, re := range leftovers {
						found[name] += len(re.FindAllString(txt, -1))
					}
				}
			}
		}
		store.Close()
		t.Logf("redact=%-5v  scan %v  %.1fMB  marks=%d  still exposed=%v  \"task-\" intact=%d",
			on, elapsed.Round(time.Millisecond), float64(chars)/1e6, marks, found, taskWords)
	}
}
