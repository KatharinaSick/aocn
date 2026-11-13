package doors

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ParseHints reads a hints.yaml: a list of hints, in order from a nudge to nearly the
// solution. Each is plain text, which aocn hint shows as it is.
func ParseHints(data []byte) ([]string, error) {
	var hints []string
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&hints); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("hints.yaml is empty")
		}
		return nil, err
	}
	var rest yaml.Node
	if err := decoder.Decode(&rest); err == nil {
		return nil, errors.New("hints.yaml must be a single YAML document")
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(hints) == 0 {
		return nil, errors.New("hints.yaml has no hints")
	}
	for i, h := range hints {
		if strings.TrimSpace(h) == "" {
			return nil, fmt.Errorf("hints.yaml: hint %d is empty", i+1)
		}
	}
	return hints, nil
}

// HintsName is where the upload puts a door's hints.
func HintsName(door int) string { return fmt.Sprintf("doors/%02d/hints.yaml", door) }

// Hints reads each door's hints from storage and keeps them for a minute, like Checks. It
// never decides whether they may be handed out.
type Hints struct {
	Storage Storage
	cache   cache[[]string]
}

// Get returns the door's hints. ErrNotFound means they were never uploaded.
func (h *Hints) Get(ctx context.Context, door int, now time.Time) ([]string, error) {
	return h.cache.get(door, now, func() ([]string, error) {
		data, err := readAll(ctx, h.Storage, HintsName(door))
		if err != nil {
			return nil, err
		}
		hints, err := ParseHints(data)
		if err != nil {
			return nil, fmt.Errorf("door %d: %w", door, err)
		}
		return hints, nil
	})
}
