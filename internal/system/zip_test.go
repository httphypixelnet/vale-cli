package system

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// zipWith builds an archive holding the given name/content pairs.
func zipWith(t *testing.T, entries map[string]string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating archive: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, body := range entries {
		e, werr := w.Create(name)
		if werr != nil {
			t.Fatalf("adding %q: %v", name, werr)
		}
		if _, werr = e.Write([]byte(body)); werr != nil {
			t.Fatalf("writing %q: %v", name, werr)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatalf("closing archive: %v", err)
	}

	return path
}

func TestUnarchive(t *testing.T) {
	src := zipWith(t, map[string]string{
		"a.txt":     "alpha",
		"sub/b.txt": "beta",
	})
	dest := t.TempDir()

	if err := Unarchive(src, dest); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}

	for name, want := range map[string]string{
		"a.txt": "alpha", filepath.Join("sub", "b.txt"): "beta",
	} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Errorf("reading %q: %v", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%q = %q, want %q", name, got, want)
		}
	}
}

// An entry naming a path outside the destination must be refused rather than
// written, however it spells the escape.
func TestUnarchiveRefusesEscapingPaths(t *testing.T) {
	for _, name := range []string{
		"../escaped.txt",
		"sub/../../escaped.txt",
	} {
		t.Run(name, func(t *testing.T) {
			src := zipWith(t, map[string]string{name: "nope"})
			dest := t.TempDir()

			if err := Unarchive(src, dest); err == nil {
				t.Errorf("expected an error for %q", name)
			}
			outside := filepath.Join(filepath.Dir(dest), "escaped.txt")
			if _, err := os.Stat(outside); err == nil {
				t.Errorf("%q was written outside the destination", outside)
			}
		})
	}
}
