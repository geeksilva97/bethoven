package ai

import (
	"sync"
	"time"
)

// commentGrace is how long past its TTL a cached comment is still shown. A short
// grace means a briefly-late worker pass doesn't blank the leaderboard, while a
// long-dead worker eventually stops showing very stale roasts.
const commentGrace = time.Hour

// CommentCache holds BETanIA's current per-player comments in memory. The comment
// worker writes it; the service's CommentSource port reads it for the leaderboard.
// It is the hot read path; the comments are also written through to the DB (via the
// worker's Save seams) so a restart restores them instead of regenerating from
// scratch. Concurrency-safe.
type CommentCache struct {
	mu sync.RWMutex
	m  map[int64]Comment
}

// NewCommentCache returns an empty cache.
func NewCommentCache() *CommentCache {
	return &CommentCache{m: make(map[int64]Comment)}
}

// Replace swaps in a fresh set of comments, dropping the previous pass's entirely
// (so a player who no longer has a story stops showing a stale line).
func (c *CommentCache) Replace(comments []Comment) {
	m := make(map[int64]Comment, len(comments))
	for _, cm := range comments {
		m[cm.UserID] = cm
	}
	c.mu.Lock()
	c.m = m
	c.mu.Unlock()
}

// Upsert replaces a single player's comment in place, leaving the rest untouched —
// used by the admin "regenerate this one" action so only the targeted line changes.
func (c *CommentCache) Upsert(cm Comment) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = make(map[int64]Comment)
	}
	c.m[cm.UserID] = cm
}

// Empty reports whether the cache holds no comments — used at boot to decide
// whether the worker must regenerate (empty ⇒ first fill) or can skip the pass
// because persisted comments were already restored.
func (c *CommentCache) Empty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m) == 0
}

// All returns the current comments keyed by user id (implements
// service.CommentSource). Entries past ExpiresAt+grace are omitted so a dead
// worker eventually shows nothing rather than ancient roasts. now is supplied by
// the caller so the read uses the service clock.
func (c *CommentCache) All(now time.Time) map[int64]Comment {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[int64]Comment, len(c.m))
	for id, cm := range c.m {
		if !cm.ExpiresAt.IsZero() && now.After(cm.ExpiresAt.Add(commentGrace)) {
			continue
		}
		out[id] = cm
	}
	return out
}
