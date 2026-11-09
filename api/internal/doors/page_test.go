package doors

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePage(t *testing.T) {
	p, err := ParsePage([]byte("title: Door\nstory: |\n  One.\n\n  Two.\nlog: |\n  Noted.\nvideoId: abc\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := Page{Title: "Door", Story: "One.\n\nTwo.", Log: "Noted.", VideoID: "abc"}
	if p != want {
		t.Errorf("got %+v, want %+v", p, want)
	}
}

func TestParsePageRejects(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"no title":         "story: s\n",
		"no story":         "title: t\n",
		"unknown key":      "title: t\nstory: s\nsotry: s\n",
		"two documents":    "title: t\nstory: s\n---\ntitle: u\n",
		"trailing ---":     "title: t\nstory: s\n---\n",
		"not a mapping":    "- title\n",
		"blank title only": "title: \"  \"\nstory: s\n",
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
	storage := &fakeStorage{files: map[string]string{"doors/01/page.yaml": "title: Old\nstory: s\n"}}
	pages := &Pages{Storage: storage}
	start := time.Date(2026, 12, 1, 5, 0, 0, 0, time.UTC)

	if p, _ := pages.Get(context.Background(), 1, start); p.Title != "Old" {
		t.Fatalf("got %q", p.Title)
	}
	storage.files["doors/01/page.yaml"] = "title: New\nstory: s\n"

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
	storage.files["doors/01/page.yaml"] = "title: t\nstory: s\n"
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
