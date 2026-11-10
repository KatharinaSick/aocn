package doors

import (
	"context"
	"io"
	"sync"
	"time"
)

// ttl is how long a door's file is kept before it's read from storage again, so a new upload
// shows up within about a minute without every request reading the bucket.
const ttl = time.Minute

// cache keeps one parsed file per door. Errors are not kept, so a fixed upload shows up on the
// next request.
type cache[T any] struct {
	mu      sync.Mutex
	entries map[int]cached[T]
}

type cached[T any] struct {
	value    T
	loadedAt time.Time
}

func (c *cache[T]) get(door int, now time.Time, load func() (T, error)) (T, error) {
	c.mu.Lock()
	entry, ok := c.entries[door]
	c.mu.Unlock()
	if ok && now.Sub(entry.loadedAt) < ttl && !now.Before(entry.loadedAt) {
		return entry.value, nil
	}

	value, err := load()
	if err != nil {
		var zero T
		return zero, err
	}

	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[int]cached[T]{}
	}
	c.entries[door] = cached[T]{value: value, loadedAt: now}
	c.mu.Unlock()
	return value, nil
}

// readAll reads a small uploaded file. Pages and checks are a few KB; 1 MB is plenty.
func readAll(ctx context.Context, s Storage, name string) ([]byte, error) {
	r, err := s.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 1<<20))
}
