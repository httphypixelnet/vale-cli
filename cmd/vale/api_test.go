package main

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/errata-ai/vale/v3/internal/system"
)

func zipFixture(t *testing.T, name, contents string) []byte {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchReplacesInvalidCacheAndReusesArchive(t *testing.T) {
	archive := zipFixture(t, "Example/Rule.yml", "extends: existence\n")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write(archive)
	}))
	defer server.Close()

	root := t.TempDir()
	cachePath := filepath.Join(root, "cache", "Example.zip")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cachePath, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}

	firstDst := filepath.Join(root, "first")
	if err := fetch(server.URL, firstDst, cachePath); err != nil {
		t.Fatalf("fetch with invalid cache: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests after invalid cache = %d, want 1", requests)
	}
	if !system.FileExists(filepath.Join(firstDst, "Example", "Rule.yml")) {
		t.Fatal("downloaded archive was not extracted")
	}

	secondDst := filepath.Join(root, "second")
	if err := fetch(server.URL, secondDst, cachePath); err != nil {
		t.Fatalf("fetch with valid cache: %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests after cache hit = %d, want 1", requests)
	}
	if !system.FileExists(filepath.Join(secondDst, "Example", "Rule.yml")) {
		t.Fatal("cached archive was not extracted")
	}
}
