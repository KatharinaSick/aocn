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

// Page is what the website shows for an open door, from the door's page.yaml.
type Page struct {
	Title string `yaml:"title" json:"title"`
	// Story and Log are plain text, paragraphs separated by a blank line.
	Story   string `yaml:"story" json:"story"`
	Log     string `yaml:"log" json:"log"`
	VideoID string `yaml:"videoId" json:"videoId"`
}

// ParsePage reads a page.yaml. Like the schedule, a misspelled or unknown key is an error,
// so a typo shows up as a broken page rather than as a missing story.
func ParsePage(data []byte) (Page, error) {
	var p Page
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&p); err != nil {
		if errors.Is(err, io.EOF) {
			return Page{}, errors.New("page.yaml is empty")
		}
		return Page{}, err
	}
	var rest yaml.Node
	if err := decoder.Decode(&rest); err == nil {
		return Page{}, errors.New("page.yaml must be a single YAML document")
	} else if !errors.Is(err, io.EOF) {
		return Page{}, err
	}

	p.Title = strings.TrimSpace(p.Title)
	p.Story = strings.TrimSpace(p.Story)
	p.Log = strings.TrimSpace(p.Log)
	p.VideoID = strings.TrimSpace(p.VideoID)
	if p.Title == "" {
		return Page{}, errors.New("page.yaml: title is required")
	}
	if p.Story == "" {
		return Page{}, errors.New("page.yaml: story is required")
	}
	return p, nil
}

// Pages reads pages from storage and keeps each one for a minute. It never decides whether a
// page may be shown.
type Pages struct {
	Storage Storage
	cache   cache[Page]
}

// Get returns the door's page. ErrNotFound means the door's page was never uploaded.
func (p *Pages) Get(ctx context.Context, door int, now time.Time) (Page, error) {
	return p.cache.get(door, now, func() (Page, error) {
		data, err := readAll(ctx, p.Storage, PageName(door))
		if err != nil {
			return Page{}, err
		}
		page, err := ParsePage(data)
		if err != nil {
			return Page{}, fmt.Errorf("door %d: %w", door, err)
		}
		return page, nil
	})
}
