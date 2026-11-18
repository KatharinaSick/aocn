package door

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

type entry struct {
	name string
	mode int64
	dir  bool
}

// writeBundle writes a .tar.gz with the entries and returns its path.
func writeBundle(t *testing.T, entries ...entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: tar.TypeReg, Size: int64(len(e.name))}
		if e.dir {
			h.Typeflag, h.Size = tar.TypeDir, 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if !e.dir {
			tw.Write([]byte(e.name))
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return path
}

func TestBundleFolder(t *testing.T) {
	tests := []struct {
		name    string
		entries []entry
		number  int
		want    string
		wantErr bool
	}{
		{name: "valid", number: 1, want: "01-example",
			entries: []entry{{name: "01-example/", dir: true}, {name: "01-example/setup.sh", mode: 0o755}}},
		{name: "leading ./", number: 1, want: "01-example",
			entries: []entry{{name: "./01-example/README.md", mode: 0o644}}},
		{name: "two digits", number: 12, want: "12-example",
			entries: []entry{{name: "12-example/README.md", mode: 0o644}}},
		{name: "other door", number: 2, wantErr: true,
			entries: []entry{{name: "01-example/README.md", mode: 0o644}}},
		{name: "two folders", number: 1, wantErr: true,
			entries: []entry{{name: "01-example/README.md", mode: 0o644}, {name: "01-other/README.md", mode: 0o644}}},
		{name: "bad folder name", number: 1, wantErr: true,
			entries: []entry{{name: "door-1/README.md", mode: 0o644}}},
		{name: "outside the folder", number: 1, wantErr: true,
			entries: []entry{{name: "01-example/../../evil.sh", mode: 0o755}}},
		{name: "absolute path", number: 1, wantErr: true,
			entries: []entry{{name: "/etc/evil.sh", mode: 0o755}}},
		{name: "empty", number: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BundleFolder(writeBundle(t, tt.entries...), tt.number)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtract(t *testing.T) {
	bundle := writeBundle(t,
		entry{name: "01-example/", dir: true},
		entry{name: "01-example/setup.sh", mode: 0o755},
		entry{name: "01-example/manifests/policy.yaml", mode: 0o644},
		entry{name: "01-example/README.md", mode: 0o644},
	)
	station := t.TempDir()
	// The station's "your station is starting" page, which the door's README replaces.
	if err := os.WriteFile(filepath.Join(station, "README.md"), []byte("starting"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Extract(bundle, station); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(station, "setup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("setup.sh lost its executable bit: %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(station, "manifests", "policy.yaml")); err != nil {
		t.Error(err)
	}
	if data, _ := os.ReadFile(filepath.Join(station, "README.md")); string(data) != "01-example/README.md" {
		t.Errorf("README.md is still the station's: %q", data)
	}
	if _, err := os.Stat(filepath.Join(station, "01-example")); err == nil {
		t.Error("the bundle's folder was unpacked too; the files belong right in the station")
	}
}

func TestRefresh(t *testing.T) {
	station := t.TempDir()
	if err := Extract(writeBundle(t,
		entry{name: "01-example/manifests/policy.yaml", mode: 0o644},
		entry{name: "01-example/README.md", mode: 0o644},
		entry{name: "01-example/.aocn/init.sh", mode: 0o755},
		entry{name: "01-example/.aocn/old.sh", mode: 0o755},
	), station); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(station, "manifests", "policy.yaml")
	readme := filepath.Join(station, "README.md")
	for _, f := range []string{policy, readme} {
		if err := os.WriteFile(f, []byte("changed"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	newer := writeBundle(t,
		entry{name: "01-example/manifests/policy.yaml", mode: 0o644},
		entry{name: "01-example/README.md", mode: 0o644},
		entry{name: "01-example/.aocn/init.sh", mode: 0o755},
		entry{name: "01-example/.aocn/workloads/app.yaml", mode: 0o644},
	)
	if err := Refresh(newer, station); err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(policy); string(data) != "changed" {
		t.Errorf("the player's file changed: %q", data)
	}
	if data, _ := os.ReadFile(readme); string(data) != "01-example/README.md" {
		t.Errorf("README.md is aocn's and should be the newest: %q", data)
	}
	if _, err := os.Stat(filepath.Join(station, ".aocn", "workloads", "app.yaml")); err != nil {
		t.Errorf("the newer .aocn wasn't unpacked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(station, ".aocn", "old.sh")); err == nil {
		t.Error("a file that's not in the newer bundle is still in .aocn")
	}
}

func TestStrangers(t *testing.T) {
	bundle := writeBundle(t,
		entry{name: "01-example/manifests/policy.yaml", mode: 0o644},
		entry{name: "01-example/.aocn/init.sh", mode: 0o755},
	)
	station := t.TempDir()
	// The station's own files, and the door's from an open that failed halfway.
	for _, name := range []string{".gitignore", "README.md", ".open.json", ".hints-seen.json", ".old", "manifests"} {
		if err := os.MkdirAll(filepath.Join(station, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := Strangers(bundle, station); err != nil || len(got) != 0 {
		t.Fatalf("a station with only its own and the door's files: got %v, %v", got, err)
	}

	if err := os.WriteFile(filepath.Join(station, "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Strangers(bundle, station); err != nil || len(got) != 1 || got[0] != "notes.txt" {
		t.Errorf("got %v, %v, want [notes.txt]", got, err)
	}

	if got, err := Strangers(bundle, filepath.Join(station, "not-there")); err != nil || got != nil {
		t.Errorf("a station that doesn't exist yet: got %v, %v", got, err)
	}
}
