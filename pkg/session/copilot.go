package session

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// GitHub Copilot's CLI keeps a session's metadata in `~/.copilot/session-store.db`
// and its transcript in `~/.copilot/session-state/<id>/events.jsonl`, and the two
// disagree: the database's `turns` table lags behind — a session can hold a whole
// conversation in its events file while `turns` still says nothing happened. So
// the events file wins wherever there is one, and `turns` is the fallback for a
// session that has no state directory at all, which is how a run hosted by
// GitHub rather than by this machine arrives.

// DefaultCopilotDirs returns the standard locations of a Copilot home, most
// likely first. Paths are returned whether or not they exist.
func DefaultCopilotDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".copilot"))
	}
	return dirs
}

// cpSessionRow is one row of the `sessions` table.
type cpSessionRow struct {
	ID         string
	CWD        string
	Repository string
	HostType   string
	Branch     string
	Summary    string
	Created    string
	Updated    string
}

// cpTurn is one row of `turns`: a prompt and the reply to it, with no record
// of anything that happened in between.
type cpTurn struct {
	User      string
	Assistant string
	Timestamp string
}

// cpEvent is one line of events.jsonl. Every line carries the same envelope and
// a payload whose shape depends on the type.
type cpEvent struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// cpMessage is a user.message or an assistant.message.
type cpMessage struct {
	// Content is what was actually said. TransformedContent is the same text
	// with a <current_datetime> preamble stapled on for the model, and showing
	// that instead puts machinery in front of every prompt.
	Content string `json:"content"`
	Model   string `json:"model"`
}

// cpUsage is a session.usage_record: the only place Copilot records what a
// turn cost, and the only source here that reports a full token breakdown AND
// a real price.
type cpUsage struct {
	Usage struct {
		Model            string  `json:"model"`
		ModelDisplayName string  `json:"modelDisplayName"`
		InputTokens      int     `json:"inputTokens"`
		OutputTokens     int     `json:"outputTokens"`
		CacheReadTokens  int     `json:"cacheReadTokens"`
		CacheWriteTokens int     `json:"cacheWriteTokens"`
		Cost             float64 `json:"cost"`
		Duration         int     `json:"duration"`
	} `json:"usage"`
}

// cpStart is a session.start, which is where the working directory is recorded
// when the database row does not have one.
type cpStart struct {
	Context struct {
		CWD string `json:"cwd"`
	} `json:"context"`
}

// ParseCopilotDir reads a Copilot home and returns its sessions. A home with
// no database yields none, without an error.
func ParseCopilotDir(dir string) ([]*Session, error) {
	dbPath := filepath.Join(dir, "session-store.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nil
	}

	db, err := openReadOnly(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := loadCPSessions(db)
	if err != nil {
		return nil, err
	}
	turns := loadCPTurns(db)

	var sessions []*Session
	for _, row := range rows {
		sess := buildCPSession(row, turns[row.ID], dir, dbPath)
		if len(sess.Events) == 0 {
			continue
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

func loadCPSessions(db *sql.DB) ([]cpSessionRow, error) {
	rows, err := db.Query(`
		SELECT id, cwd, repository, host_type, branch, summary, created_at, updated_at
		FROM sessions
		ORDER BY updated_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []cpSessionRow
	for rows.Next() {
		var r cpSessionRow
		var cwd, repo, host, branch, summary, created, updated sql.NullString
		if err := rows.Scan(&r.ID, &cwd, &repo, &host, &branch, &summary, &created, &updated); err != nil {
			continue
		}
		r.CWD, r.Repository, r.HostType = cwd.String, repo.String, host.String
		r.Branch, r.Summary = branch.String, summary.String
		r.Created, r.Updated = created.String, updated.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func loadCPTurns(db *sql.DB) map[string][]cpTurn {
	rows, err := db.Query(`
		SELECT session_id, user_message, assistant_response, timestamp
		FROM turns
		ORDER BY session_id, turn_index`)
	if err != nil {
		return map[string][]cpTurn{}
	}
	defer rows.Close()

	out := make(map[string][]cpTurn)
	for rows.Next() {
		var id string
		var user, assistant, ts sql.NullString
		if err := rows.Scan(&id, &user, &assistant, &ts); err != nil {
			continue
		}
		out[id] = append(out[id], cpTurn{
			User: user.String, Assistant: assistant.String, Timestamp: ts.String,
		})
	}
	return out
}

// readCPEvents reads a session's event stream. A missing file is not an error:
// a session hosted by GitHub never writes one.
func readCPEvents(dir, id string) []cpEvent {
	f, err := os.Open(filepath.Join(dir, "session-state", id, "events.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []cpEvent
	sc := bufio.NewScanner(f)
	// An event carrying a whole model response can be far longer than the
	// default 64KB token.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e cpEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		out = append(out, e)
	}
	return out
}

func buildCPSession(row cpSessionRow, turns []cpTurn, dir, dbPath string) *Session {
	sess := &Session{Info: SessionInfo{ID: "cp-" + row.ID, Source: "copilot"}}
	sess.Info.ToolCounts = make(map[string]int)

	if events := readCPEvents(dir, row.ID); len(events) > 0 {
		emitCPEvents(events, sess)
	} else {
		emitCPTurns(turns, sess)
	}

	finishCPSession(sess, row, dbPath)
	return sess
}

// emitCPEvents turns an events.jsonl into a timeline.
//
// Only the types that carry what was said are read. The rest are machinery —
// turn boundaries, model-call bookkeeping, permission notices — and the
// session's own counters already say what they would.
func emitCPEvents(events []cpEvent, sess *Session) {
	for _, e := range events {
		ts := cpTime(e.Timestamp)

		switch e.Type {
		case "session.start":
			var s cpStart
			if json.Unmarshal(e.Data, &s) == nil && s.Context.CWD != "" {
				sess.Info.CWD = s.Context.CWD
			}

		case "user.message":
			var m cpMessage
			if json.Unmarshal(e.Data, &m) != nil || strings.TrimSpace(m.Content) == "" {
				continue
			}
			sess.Info.UserPrompts++
			sess.Events = append(sess.Events,
				Event{Type: EventUserPrompt, Timestamp: ts, UserText: m.Content})

		case "assistant.message":
			// The reply, and the ONLY event that holds it. `model.message` and
			// `model.response` look like they should and do not: they are the
			// side call that writes the session's own title, and their content
			// is a <session-title> tag. Reading those puts markup on screen
			// where the answer belongs.
			var m cpMessage
			if json.Unmarshal(e.Data, &m) != nil || strings.TrimSpace(m.Content) == "" {
				continue
			}
			if m.Model != "" && sess.Info.Model == "" {
				sess.Info.Model = m.Model
			}
			sess.Events = append(sess.Events,
				Event{Type: EventText, Timestamp: ts, Text: m.Content})

		case "session.usage_record":
			var u cpUsage
			if json.Unmarshal(e.Data, &u) != nil {
				continue
			}
			sess.Info.InputTokens += u.Usage.InputTokens
			sess.Info.OutputTokens += u.Usage.OutputTokens
			sess.Info.CacheReadTokens += u.Usage.CacheReadTokens
			sess.Info.CacheWriteTokens += u.Usage.CacheWriteTokens
			sess.Info.CostUSD += u.Usage.Cost
			sess.Info.ActiveDuration += time.Duration(u.Usage.Duration) * time.Millisecond
			if sess.Info.Model == "" {
				sess.Info.Model = u.Usage.Model
			}

		case "session.error":
			// Counted, not drawn. A rate limit is why a prompt has no reply,
			// and the overview says so — but EventSystem renders as "* system"
			// with no text, so an event here would be an invisible row.
			//
			// `model.turn_failed` is NOT counted beside it: a failed turn
			// raises both, so counting the pair reported five failures for
			// three rate-limited prompts.
			sess.Info.Errors++
		}
	}
}

// emitCPTurns is the fallback for a session with no event stream: the
// database's own record of each turn, which is a prompt and a reply and
// nothing else — no tool calls, no files, no timing.
func emitCPTurns(turns []cpTurn, sess *Session) {
	for _, t := range turns {
		ts := cpTime(t.Timestamp)
		if strings.TrimSpace(t.User) != "" {
			sess.Info.UserPrompts++
			sess.Events = append(sess.Events,
				Event{Type: EventUserPrompt, Timestamp: ts, UserText: t.User})
		}
		if strings.TrimSpace(t.Assistant) != "" {
			sess.Events = append(sess.Events,
				Event{Type: EventText, Timestamp: ts, Text: t.Assistant})
		}
	}
}

func finishCPSession(sess *Session, row cpSessionRow, dbPath string) {
	if row.CWD != "" {
		sess.Info.CWD = row.CWD
	}
	sess.Info.ProjectDir = sess.Info.CWD
	if sess.Info.CWD != "" {
		sess.Info.ProjectName = filepath.Base(sess.Info.CWD)
	}
	sess.Info.FilePath = dbPath
	sess.Info.GitBranch = row.Branch
	sess.Info.StartTime = cpTime(row.Created)
	sess.Info.LastUpdate = cpTime(row.Updated)
	sess.Info.EventCount = len(sess.Events)

	// Copilot writes its own summary through a side model call — but a run
	// hosted by GitHub gets the whole issue body instead, newlines and all,
	// and a title with a newline in it costs the list a row it did not count.
	sess.Info.Title = firstLineOf(strings.TrimSpace(row.Summary))
	if sess.Info.Title == "" {
		sess.Info.Title = deriveTitle(sess.Events)
	}

	if sess.Info.StartTime.IsZero() && len(sess.Events) > 0 {
		sess.Info.StartTime = sess.Events[0].Timestamp
	}
}

// cpTime parses the ISO 8601 timestamps Copilot writes everywhere, in the
// database and in the event stream alike.
func cpTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
