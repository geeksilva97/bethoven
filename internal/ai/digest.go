package ai

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// RecentLiveComments reads BETanIA's live-commentary lines back from the comment
// log — the durable record — returning those logged at or after `since`, oldest
// first, capped to the most recent `max`. This is how a finished match's "story"
// survives: the live worker discards its in-memory lines the moment a game ends,
// but every line was logged (source:"live_comment"), so the post-match digest can
// still recover the play-by-play. A missing/unreadable log yields nil, never an
// error (the digest is best-effort).
func RecentLiveComments(path string, since time.Time, max int) []string {
	if path == "" || max <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var e commentLogEntry
		if err := json.Unmarshal([]byte(text), &e); err != nil {
			continue
		}
		if e.Source != "live_comment" || e.Text == "" {
			continue
		}
		if at, err := time.Parse(time.RFC3339, e.At); err == nil && at.Before(since) {
			continue
		}
		lines = append(lines, e.Text)
	}
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return lines
}
