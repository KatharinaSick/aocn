package doors

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Page is what the website shows for an open door, from the door's page.yaml. The page is
// all a player reads, so everything here is short, and the limits keep it that way.
type Page struct {
	Title string `yaml:"title" json:"title"`
	// Story is POLARIS-7's log entry for the door, in her words, signed by her on the page:
	// plain text, paragraphs separated by a blank line.
	Story string `yaml:"story" json:"story"`
	// Mission is what to fix, one short line each.
	Mission []string `yaml:"mission" json:"mission"`
	// Pola is POLARIS-7's one sentence for the door, her prompt line on the page.
	Pola    string `yaml:"pola" json:"pola"`
	VideoID string `yaml:"videoId" json:"videoId"`
	// Maintainer is who the interview is with, thanked under the video.
	Maintainer Maintainer `yaml:"maintainer" json:"maintainer"`
	// Docs are a few links that help with the mission.
	Docs []Link `yaml:"docs" json:"docs"`
}

// Maintainer is the person in a door's interview.
type Maintainer struct {
	Name string `yaml:"name" json:"name"`
	// Role is what they do, like "Cilium maintainer".
	Role string `yaml:"role" json:"role"`
	// URL is where to find them, like their GitHub profile.
	URL string `yaml:"url" json:"url"`
}

// Link is a page someone can open from the door page.
type Link struct {
	Title string `yaml:"title" json:"title"`
	URL   string `yaml:"url" json:"url"`
}

// What fits on the page without scrolling, in characters.
const (
	maxStory        = 500
	maxMission      = 4
	maxMissionGoal  = 120
	maxPolaSentence = 120
	maxDocs         = 3
	maxLinkTitle    = 60
)

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
	p.Pola = strings.TrimSpace(p.Pola)
	p.VideoID = strings.TrimSpace(p.VideoID)
	for i := range p.Mission {
		p.Mission[i] = strings.TrimSpace(p.Mission[i])
	}
	p.Maintainer.Name = strings.TrimSpace(p.Maintainer.Name)
	p.Maintainer.Role = strings.TrimSpace(p.Maintainer.Role)
	p.Maintainer.URL = strings.TrimSpace(p.Maintainer.URL)
	for i := range p.Docs {
		p.Docs[i].Title = strings.TrimSpace(p.Docs[i].Title)
		p.Docs[i].URL = strings.TrimSpace(p.Docs[i].URL)
	}
	return p, p.validate()
}

func (p Page) validate() error {
	switch {
	case p.Title == "":
		return errors.New("page.yaml: title is required")
	case p.Story == "":
		return errors.New("page.yaml: story is required")
	case utf8.RuneCountInString(p.Story) > maxStory:
		return fmt.Errorf("page.yaml: story is %d characters, keep it to %d", utf8.RuneCountInString(p.Story), maxStory)
	case len(p.Mission) == 0:
		return errors.New("page.yaml: mission is required: a list of what to fix")
	case len(p.Mission) > maxMission:
		return fmt.Errorf("page.yaml: mission has %d lines, keep it to %d", len(p.Mission), maxMission)
	case p.Pola == "":
		return errors.New("page.yaml: pola is required: her one sentence for the door")
	case utf8.RuneCountInString(p.Pola) > maxPolaSentence:
		return fmt.Errorf("page.yaml: pola is %d characters, keep it to %d", utf8.RuneCountInString(p.Pola), maxPolaSentence)
	case strings.Contains(p.Pola, "\n"):
		return errors.New("page.yaml: pola is one line")
	}
	switch {
	case p.Maintainer.Name == "":
		return errors.New("page.yaml: maintainer.name is required: who the interview is with")
	case p.Maintainer.Role == "":
		return errors.New("page.yaml: maintainer.role is required, like \"Cilium maintainer\"")
	case !isLink(p.Maintainer.URL):
		return fmt.Errorf("page.yaml: maintainer.url must be an https link, got %q", p.Maintainer.URL)
	case len(p.Docs) > maxDocs:
		return fmt.Errorf("page.yaml: docs has %d links, keep it to %d", len(p.Docs), maxDocs)
	}
	for i, doc := range p.Docs {
		switch {
		case doc.Title == "":
			return fmt.Errorf("page.yaml: docs link %d has no title", i+1)
		case utf8.RuneCountInString(doc.Title) > maxLinkTitle:
			return fmt.Errorf("page.yaml: docs link %d's title is %d characters, keep it to %d", i+1, utf8.RuneCountInString(doc.Title), maxLinkTitle)
		case !isLink(doc.URL):
			return fmt.Errorf("page.yaml: docs link %d must be an https link, got %q", i+1, doc.URL)
		}
	}
	for i, goal := range p.Mission {
		switch {
		case goal == "":
			return fmt.Errorf("page.yaml: mission line %d is empty", i+1)
		case utf8.RuneCountInString(goal) > maxMissionGoal:
			return fmt.Errorf("page.yaml: mission line %d is %d characters, keep it to %d", i+1, utf8.RuneCountInString(goal), maxMissionGoal)
		case strings.Contains(goal, "\n"):
			return fmt.Errorf("page.yaml: mission line %d is one line", i+1)
		}
	}
	return nil
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

// isLink reports whether s is an absolute https URL, the only kind the page links to.
func isLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && !strings.ContainsAny(s, " \n")
}
