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
	)
	dir := t.TempDir()
	if err := Extract(bundle, dir); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, "01-example", "setup.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("setup.sh lost its executable bit: %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(dir, "01-example", "manifests", "policy.yaml")); err != nil {
		t.Error(err)
	}
	// Only the door's folder is there.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("want only 01-example in %s, got %d entries", dir, len(entries))
	}
}

func TestRefresh(t *testing.T) {
	dir := t.TempDir()
	if err := Extract(writeBundle(t,
		entry{name: "01-example/manifests/policy.yaml", mode: 0o644},
		entry{name: "01-example/.aocn/verify.sh", mode: 0o755},
		entry{name: "01-example/.aocn/old.sh", mode: 0o755},
	), dir); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(dir, "01-example", "manifests", "policy.yaml")
	if err := os.WriteFile(policy, []byte("the player's change"), 0o644); err != nil {
		t.Fatal(err)
	}

	newer := writeBundle(t,
		entry{name: "01-example/manifests/policy.yaml", mode: 0o644},
		entry{name: "01-example/.aocn/verify.sh", mode: 0o755},
		entry{name: "01-example/.aocn/workloads/app.yaml", mode: 0o644},
	)
	if err := Refresh(newer, dir, "01-example"); err != nil {
		t.Fatal(err)
	}

	if data, _ := os.ReadFile(policy); string(data) != "the player's change" {
		t.Errorf("the player's file changed: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, "01-example", ".aocn", "workloads", "app.yaml")); err != nil {
		t.Errorf("the newer .aocn wasn't unpacked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "01-example", ".aocn", "old.sh")); err == nil {
		t.Error("a file that's not in the newer bundle is still in .aocn")
	}
}
