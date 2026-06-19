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
// Nothing is persisted — a restart starts empty and the first pass refills it,
// mirroring live.Cache. Concurrency-safe.
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
