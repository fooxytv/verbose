package session

import (
	"io"
	"os"
	"sync"
	"time"
)

// liveIdle is how long a transcript's parse is kept after it was last read.
// A session that is being worked in is written every few seconds; one left
// this long has finished, or paused long enough that a single full parse when
// it resumes costs nothing that matters.
const liveIdle = 15 * time.Minute

// liveTranscripts keeps the parse of each transcript that is being written to,
// so a change reads only the lines added since the last one.
//
// Re-parsing the whole file on every write was the cost of a live session: a
// long one runs to hundreds of megabytes, about five seconds of CPU to parse,
// and the watcher reloads a busy file every two seconds — in every process
// holding a Store. Only transcripts written recently are kept, so the parses of
// the whole history are not held twice in memory.
type liveTranscripts struct {
	mu    sync.Mutex
	files map[string]*liveTranscript
}

type liveTranscript struct {
	mu     sync.Mutex // held across a read, so two reloads cannot interleave
	parser *transcriptParser
	used   time.Time // guarded by liveTranscripts.mu
}

// get returns the entry for path, creating it if needed, and forgets those
// left idle.
func (l *liveTranscripts) get(path string) *liveTranscript {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.files == nil {
		l.files = make(map[string]*liveTranscript)
	}
	for p, t := range l.files {
		if p != path && now.Sub(t.used) > liveIdle {
			delete(l.files, p)
		}
	}
	t := l.files[path]
	if t == nil {
		t = &liveTranscript{}
		l.files[path] = t
	}
	t.used = now
	return t
}

// forget drops the parse of path, for a transcript that has gone.
func (l *liveTranscripts) forget(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.files, path)
}

// parseLive parses a transcript the way ParseSessionFile does, reading only
// what was appended since the last call for the same path.
//
// Claude Code only ever appends to a transcript. A file that is shorter than
// what was already read has been replaced, and is read again from the start.
func (s *Store) parseLive(path string) (*Session, error) {
	t := s.live.get(path)
	t.mu.Lock()
	defer t.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		t.parser = nil
		s.live.forget(path)
		return nil, err
	}
	defer f.Close()

	if t.parser != nil {
		info, err := f.Stat()
		if err != nil || info.Size() < t.parser.offset {
			t.parser = nil
		}
	}
	if t.parser == nil {
		t.parser = newTranscriptParser(path)
	}
	if _, err := f.Seek(t.parser.offset, io.SeekStart); err != nil {
		t.parser = nil
		s.live.forget(path)
		return ParseSessionFile(path)
	}
	t.parser.read(f)
	return t.parser.session(true), nil
}

// recentlyWritten reports whether path changed within liveIdle, which makes it
// worth keeping its parse from the start.
func recentlyWritten(path string) bool {
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) < liveIdle
}
