package doors

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// minimalPage has every field a page needs, and nothing else.
const minimalPage = "title: t\nstory: s\nmission: [m]\npola: p\nmaintainer: {name: n, role: r, url: 'https://example.com/n'}\n"

// maintainer completes a page that's built inline.
const maintainer = "maintainer: {name: n, role: r, url: 'https://example.com/n'}\n"

func TestParsePage(t *testing.T) {
	p, err := ParsePage([]byte(`title: Door
story: |
  One sentence,
  wrapped.

  Two.
mission:
  - First goal
  - Second goal
pola: Noted.
videoId: abc
maintainer:
  name: Jane Doe
  role: Example maintainer
  url: https://example.com/jane
docs:
  - title: Example docs
    url: https://example.com/docs
`))
	if err != nil {
		t.Fatal(err)
	}
	want := Page{
		Title:      "Door",
		Story:      "One sentence,\nwrapped.\n\nTwo.",
		Mission:    []string{"First goal", "Second goal"},
		Pola:       "Noted.",
		VideoID:    "abc",
		Maintainer: Maintainer{Name: "Jane Doe", Role: "Example maintainer", URL: "https://example.com/jane"},
		Docs:       []Link{{Title: "Example docs", URL: "https://example.com/docs"}},
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got %+v, want %+v", p, want)
	}
}

func TestParseMinimalPage(t *testing.T) {
	p, err := ParsePage([]byte(minimalPage))
	if err != nil {
		t.Fatal(err)
	}
	if p.VideoID != "" || p.Docs != nil {
		t.Errorf("got %+v", p)
	}
}

func TestParsePageRejects(t *testing.T) {
	long := strings.Repeat("x", 501)
	cases := map[string]string{
		"empty":                   "",
		"no title":                "story: s\nmission: [m]\npola: p\n" + maintainer,
		"no story":                "title: t\nmission: [m]\npola: p\n" + maintainer,
		"no mission":              "title: t\nstory: s\npola: p\n" + maintainer,
		"empty mission":           "title: t\nstory: s\nmission: []\npola: p\n" + maintainer,
		"no pola":                 "title: t\nstory: s\nmission: [m]\n" + maintainer,
		"unknown key":             minimalPage + "sotry: s\n",
		"the old log":             minimalPage + "log: Noted.\n",
		"a full story":            minimalPage + "fullStory: More.\n",
		"two documents":           minimalPage + "---\ntitle: u\n",
		"trailing ---":            minimalPage + "---\n",
		"not a mapping":           "- title\n",
		"blank title only":        "title: \"  \"\nstory: s\nmission: [m]\npola: p\n" + maintainer,
		"key twice":               minimalPage + "title: u\n",
		"list as text":            "title: t\nstory: [s]\nmission: [m]\npola: p\n" + maintainer,
		"long story":              "title: t\nstory: " + long + "\nmission: [m]\npola: p\n" + maintainer,
		"five goals":              "title: t\nstory: s\nmission: [a, b, c, d, e]\npola: p\n" + maintainer,
		"long goal":               "title: t\nstory: s\nmission: [" + long[:121] + "]\npola: p\n" + maintainer,
		"blank goal":              "title: t\nstory: s\nmission: [m, ' ']\npola: p\n" + maintainer,
		"goal on two lines":       "title: t\nstory: s\nmission:\n  - |\n    one\n    two\npola: p\n" + maintainer,
		"long pola":               "title: t\nstory: s\nmission: [m]\npola: " + long[:121] + "\n" + maintainer,
		"pola on two lines":       "title: t\nstory: s\nmission: [m]\npola: |\n  one\n  two\n" + maintainer,
		"mission as a sentence":   "title: t\nstory: s\nmission: fix it\npola: p\n" + maintainer,
		"no maintainer":           "title: t\nstory: s\nmission: [m]\npola: p\n",
		"maintainer without role": "title: t\nstory: s\nmission: [m]\npola: p\nmaintainer: {name: n, url: 'https://example.com/n'}\n",
		"maintainer over http":    "title: t\nstory: s\nmission: [m]\npola: p\nmaintainer: {name: n, role: r, url: 'http://example.com/n'}\n",
		"four docs":               minimalPage + "docs: [{title: a, url: 'https://a.example'}, {title: b, url: 'https://b.example'}, {title: c, url: 'https://c.example'}, {title: d, url: 'https://d.example'}]\n",
		"doc without title":       minimalPage + "docs: [{url: 'https://a.example'}]\n",
		"doc that's no link":      minimalPage + "docs: [{title: a, url: 'javascript:alert(1)'}]\n",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParsePage([]byte(data)); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// fakeStorage serves files from a map and counts reads.
type fakeStorage struct {
	files map[string]string
	reads int
}

func (s *fakeStorage) Open(_ context.Context, name string) (File, error) {
	s.reads++
	data, ok := s.files[name]
	if !ok {
		return File{}, ErrNotFound
	}
	return File{ReadCloser: io.NopCloser(strings.NewReader(data)), Size: int64(len(data))}, nil
}

func TestPagesKeepsAPageForAMinute(t *testing.T) {
	storage := &fakeStorage{files: map[string]string{"doors/01/page.yaml": strings.Replace(minimalPage, "title: t", "title: Old", 1)}}
	pages := &Pages{Storage: storage}
	start := time.Date(2026, 12, 1, 5, 0, 0, 0, time.UTC)

	if p, _ := pages.Get(context.Background(), 1, start); p.Title != "Old" {
		t.Fatalf("got %q", p.Title)
	}
	storage.files["doors/01/page.yaml"] = strings.Replace(minimalPage, "title: t", "title: New", 1)

	if p, _ := pages.Get(context.Background(), 1, start.Add(59*time.Second)); p.Title != "Old" {
		t.Errorf("within a minute: got %q, want the kept page", p.Title)
	}
	if p, _ := pages.Get(context.Background(), 1, start.Add(time.Minute)); p.Title != "New" {
		t.Errorf("after a minute: got %q, want the new upload", p.Title)
	}
	if storage.reads != 2 {
		t.Errorf("read storage %d times, want 2", storage.reads)
	}
}

func TestPagesDoesNotKeepErrors(t *testing.T) {
	storage := &fakeStorage{files: map[string]string{"doors/01/page.yaml": "title: t\n"}}
	pages := &Pages{Storage: storage}
	now := time.Date(2026, 12, 1, 5, 0, 0, 0, time.UTC)

	if _, err := pages.Get(context.Background(), 1, now); err == nil {
		t.Fatal("broken page accepted")
	}
	storage.files["doors/01/page.yaml"] = minimalPage
	if _, err := pages.Get(context.Background(), 1, now); err != nil {
		t.Errorf("fixed page: %v", err)
	}

	if _, err := pages.Get(context.Background(), 2, now); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing page: got %v, want ErrNotFound", err)
	}
}

func TestDirTellsTheSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "doors", "01"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doors", "01", "bundle.tar.gz"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Dir(dir).Open(context.Background(), BundleName(1))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Size != 5 {
		t.Errorf("size %d, want 5", f.Size)
	}
}

func TestDirMissingFile(t *testing.T) {
	if _, err := Dir(t.TempDir()).Open(context.Background(), PageName(1)); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestNames(t *testing.T) {
	if got := PageName(1); got != "doors/01/page.yaml" {
		t.Errorf("PageName(1) = %q", got)
	}
	if got := BundleName(12); got != "doors/12/bundle.tar.gz" {
		t.Errorf("BundleName(12) = %q", got)
	}
}
