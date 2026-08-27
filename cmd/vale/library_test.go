package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Masterminds/semver/v3"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newMockHTTPClient(fn roundTripFunc) *http.Client {
	return &http.Client{
		Transport: fn,
	}
}

func TestReleaseUnmarshalJSON(t *testing.T) {
	t.Run("valid release with semver tag", func(t *testing.T) {
		jsonData := `{
			"url": "https://api.github.com/repos/errata-ai/Google/releases/1",
			"id": 12345,
			"tag_name": "v1.2.3",
			"target_commitish": "main",
			"name": "v1.2.3 Release",
			"body": "Release notes",
			"draft": false,
			"prerelease": false,
			"immutable": true,
			"created_at": "2026-01-01T00:00:00Z",
			"published_at": "2026-01-02T00:00:00Z",
			"assets": [
				{
					"url": "https://api.github.com/repos/errata-ai/Google/releases/assets/1",
					"browser_download_url": "https://github.com/errata-ai/Google/releases/download/v1.2.3/Google.zip",
					"id": 100,
					"node_id": "MDEyOlJlbGVhc2VBc3NldDEwMA==",
					"name": "Google.zip",
					"label": "Google Style",
					"state": "uploaded",
					"content_type": "application/zip",
					"size": 1024,
					"digest": "sha256:abc"
				}
			]
		}`

		var rel GitHubRelease
		if err := json.Unmarshal([]byte(jsonData), &rel); err != nil {
			t.Fatalf("unexpected unmarshal error: %v", err)
		}

		if rel.ID != 12345 {
			t.Errorf("expected ID 12345, got %d", rel.ID)
		}
		if rel.Name != "v1.2.3 Release" {
			t.Errorf("expected Name 'v1.2.3 Release', got %s", rel.Name)
		}
		if rel.TagName == nil || rel.TagName.String() != "1.2.3" {
			t.Errorf("expected TagName '1.2.3', got %v", rel.TagName)
		}
		if !rel.Immutable {
			t.Errorf("expected Immutable true, got %v", rel.Immutable)
		}
		if len(rel.Assets) != 1 {
			t.Fatalf("expected 1 asset, got %d", len(rel.Assets))
		}
		asset := rel.Assets[0]
		if asset.Name != "Google.zip" || asset.Size != 1024 || asset.Digest != "sha256:abc" {
			t.Errorf("unexpected asset fields: %+v", asset)
		}
	})

	t.Run("invalid json syntax", func(t *testing.T) {
		var rel GitHubRelease
		err := json.Unmarshal([]byte(`{not valid json`), &rel)
		if err == nil {
			t.Fatal("expected unmarshal error for malformed json, got nil")
		}
	})

	t.Run("non-semver tag name ignored gracefully", func(t *testing.T) {
		jsonData := `{"tag_name": "not-a-semver-version"}`
		var rel GitHubRelease
		err := json.Unmarshal([]byte(jsonData), &rel)
		if err != nil {
			t.Fatalf("unexpected unmarshal error: %v", err)
		}
		if rel.TagName != nil {
			t.Errorf("expected nil TagName for non-semver tag, got %v", rel.TagName)
		}
	})
}

func TestParseRepo(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		wantOwner   string
		wantRepo    string
		expectError bool
	}{
		{
			name:        "https github url",
			url:         "https://github.com/errata-ai/Google",
			wantOwner:   "errata-ai",
			wantRepo:    "Google",
			expectError: false,
		},
		{
			name:        "http github url",
			url:         "http://github.com/errata-ai/vale",
			wantOwner:   "errata-ai",
			wantRepo:    "vale",
			expectError: false,
		},
		{
			name:        "github url with trailing path",
			url:         "https://github.com/owner/repo/releases/tag/v1.0.0",
			wantOwner:   "owner",
			wantRepo:    "repo",
			expectError: false,
		},
		{
			name:        "non-github url",
			url:         "https://gitlab.com/owner/repo",
			expectError: true,
		},
		{
			name:        "missing repo",
			url:         "https://github.com/owner",
			expectError: true,
		},
		{
			name:        "empty string",
			url:         "",
			expectError: true,
		},
		{
			name:        "invalid url",
			url:         "not-a-valid-url",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := parseRepo(tt.url)
			if tt.expectError {
				if err == nil {
					t.Errorf("expected error for url %q, got nil result: %+v", tt.url, res)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for url %q: %v", tt.url, err)
			}
			if res.owner != tt.wantOwner || res.repo != tt.wantRepo {
				t.Errorf("parseRepo(%q) = owner:%q, repo:%q; want owner:%q, repo:%q",
					tt.url, res.owner, res.repo, tt.wantOwner, tt.wantRepo)
			}
		})
	}
}

func TestCacheDir(t *testing.T) {
	t.Run("uses VALE_CACHE environment variable", func(t *testing.T) {
		tempDir := t.TempDir()
		customCache := filepath.Join(tempDir, "custom-vale-cache")
		t.Setenv("VALE_CACHE", customCache)

		dir, err := cacheDir()
		if err != nil {
			t.Fatalf("unexpected error from cacheDir: %v", err)
		}
		if dir != customCache {
			t.Errorf("expected cacheDir to return %q, got %q", customCache, dir)
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Errorf("expected cache directory %q to be created", dir)
		}
	})

	t.Run("uses XDG_CACHE_HOME when VALE_CACHE is unset on linux", func(t *testing.T) {
		t.Setenv("VALE_CACHE", "")
		tempDir := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", tempDir)

		dir, err := cacheDir()
		if err != nil {
			t.Fatalf("unexpected error from cacheDir: %v", err)
		}
		expected := filepath.Join(tempDir, "vale-cli")
		if dir != expected {
			t.Errorf("expected cacheDir to return %q, got %q", expected, dir)
		}
	})

	t.Run("falls back to user home directory when XDG_CACHE_HOME is unset", func(t *testing.T) {
		t.Setenv("VALE_CACHE", "")
		t.Setenv("XDG_CACHE_HOME", "")
		homeDir := t.TempDir()
		t.Setenv("HOME", homeDir)

		dir, err := cacheDir()
		if err != nil {
			t.Fatalf("unexpected error from cacheDir: %v", err)
		}
		expected := filepath.Join(homeDir, ".cache", "vale-cli")
		if dir != expected {
			t.Errorf("expected cacheDir to return %q, got %q", expected, dir)
		}
	})
}

func TestLookupReleases(t *testing.T) {
	repo := RepoParseResult{owner: "test-owner", repo: "test-repo"}

	t.Run("fresh cache hit does not call http client", func(t *testing.T) {
		cache := t.TempDir()
		cacheFile := filepath.Join(cache, "test-owner.test-repo.json")

		cachedReleases := `[
			{
				"url": "https://api.github.com/repos/test-owner/test-repo/releases/1",
				"id": 1,
				"tag_name": "v1.0.0",
				"name": "v1.0.0"
			}
		]`
		if err := os.WriteFile(cacheFile, []byte(cachedReleases), 0o600); err != nil {
			t.Fatal(err)
		}

		clientCalled := false
		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			clientCalled = true
			return nil, errors.New("client should not have been called on cache hit")
		})

		releases, err := lookupReleases(repo, cache, client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if clientCalled {
			t.Error("expected http client not to be called on fresh cache")
		}
		if len(releases) != 1 || releases[0].TagName.String() != "1.0.0" {
			t.Errorf("unexpected releases returned: %+v", releases)
		}
	})

	t.Run("cache miss fetches from github api and writes to cache", func(t *testing.T) {
		cache := t.TempDir()
		cacheFile := filepath.Join(cache, "test-owner.test-repo.json")

		apiCalled := false
		client := newMockHTTPClient(func(req *http.Request) (*http.Response, error) {
			apiCalled = true
			if req.URL.Path != "/repos/test-owner/test-repo/releases" {
				t.Errorf("unexpected request path: %s", req.URL.Path)
			}
			body := `[
				{"url": "https://api.github.com/release/1", "id": 1, "tag_name": "v1.0.0"},
				{"url": "https://api.github.com/release/2", "id": 2, "tag_name": "v2.0.0"}
			]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})

		releases, err := lookupReleases(repo, cache, client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !apiCalled {
			t.Error("expected API to be called on cache miss")
		}
		if len(releases) != 2 {
			t.Fatalf("expected 2 releases, got %d", len(releases))
		}

		// Verify cache file was written
		if _, statErr := os.Stat(cacheFile); statErr != nil {
			t.Fatalf("expected cache file %s to exist: %v", cacheFile, statErr)
		}
	})

	t.Run("stale cache refetches from api", func(t *testing.T) {
		cache := t.TempDir()
		cacheFile := filepath.Join(cache, "test-owner.test-repo.json")

		if err := os.WriteFile(cacheFile, []byte(`[{"id": 1, "tag_name": "v1.0.0"}]`), 0o600); err != nil {
			t.Fatal(err)
		}
		// Set mod time to 30 minutes in the past (cache TTL is 15 minutes)
		oldTime := time.Now().Add(-30 * time.Minute)
		if err := os.Chtimes(cacheFile, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}

		apiCalled := false
		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			apiCalled = true
			body := `[{"id": 2, "tag_name": "v2.0.0"}]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})

		releases, err := lookupReleases(repo, cache, client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !apiCalled {
			t.Error("expected API to be called for stale cache")
		}
		if len(releases) != 1 || releases[0].TagName.String() != "2.0.0" {
			t.Errorf("expected updated release v2.0.0, got %+v", releases)
		}
	})

	t.Run("api error is returned", func(t *testing.T) {
		cache := t.TempDir()
		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("network failure")
		})

		_, err := lookupReleases(repo, cache, client)
		if err == nil {
			t.Fatal("expected error on network failure, got nil")
		}
	})
}

func TestGetMatchingRelease(t *testing.T) {
	repo := RepoParseResult{owner: "test-owner", repo: "test-repo"}

	setupClient := func(releasesJSON string) *http.Client {
		return newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(releasesJSON)),
				Header:     make(http.Header),
			}, nil
		})
	}

	t.Run("finds matching release for semver constraint", func(t *testing.T) {
		t.Setenv("VALE_CACHE", t.TempDir())

		body := `[
			{"url": "https://github.com/release/v1.0.0", "id": 1, "tag_name": "v1.0.0"},
			{"url": "https://github.com/release/v1.1.0", "id": 2, "tag_name": "v1.1.0"},
			{"url": "https://github.com/release/v2.0.0", "id": 3, "tag_name": "v2.0.0"}
		]`
		client := setupClient(body)

		constraint, err := semver.NewConstraint("^1.0.0")
		if err != nil {
			t.Fatal(err)
		}

		rel, err := getMatchingRelease(repo, constraint, client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rel == nil || rel.TagName.String() != "1.1.0" {
			t.Errorf("expected release v1.1.0 (latest matching ^1.0.0), got %v", rel)
		}
	})

	t.Run("returns error when no release matches constraint", func(t *testing.T) {
		t.Setenv("VALE_CACHE", t.TempDir())

		body := `[
			{"url": "https://github.com/release/v1.0.0", "id": 1, "tag_name": "v1.0.0"}
		]`
		client := setupClient(body)

		constraint, err := semver.NewConstraint(">=2.0.0")
		if err != nil {
			t.Fatal(err)
		}

		rel, err := getMatchingRelease(repo, constraint, client)
		if err == nil {
			t.Fatalf("expected error for unmatched constraint, got release: %+v", rel)
		}
		if err.Error() != "no matching release found" {
			t.Errorf("expected 'no matching release found', got %q", err.Error())
		}
	})
}

func TestGetLibrary(t *testing.T) {
	t.Run("successfully fetches and parses library styles", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `[{"name": "Google", "url": "https://github.com/errata-ai/Google"}]`)
		}))
		defer server.Close()

		origLibrary := library
		library = server.URL
		defer func() { library = origLibrary }()

		styles, err := getLibrary()
		if err != nil {
			t.Fatalf("unexpected error from getLibrary: %v", err)
		}
		if len(styles) != 1 || styles[0].Name != "Google" || styles[0].URL != "https://github.com/errata-ai/Google" {
			t.Errorf("unexpected styles: %+v", styles)
		}
	})

	t.Run("returns error on invalid library json", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintln(w, `invalid-json`)
		}))
		defer server.Close()

		origLibrary := library
		library = server.URL
		defer func() { library = origLibrary }()

		_, err := getLibrary()
		if err == nil {
			t.Fatal("expected error for malformed library json, got nil")
		}
	})
}

func setupTestLibrary(t *testing.T, libraryBody string) {
	t.Helper()
	t.Setenv("VALE_CACHE", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, libraryBody)
	}))
	t.Cleanup(server.Close)
	origLibrary := library
	library = server.URL
	t.Cleanup(func() { library = origLibrary })
}

func TestInLibrary(t *testing.T) {
	const defaultGoogleLib = `[{"name": "Google", "url": "https://github.com/errata-ai/Google"}]`

	t.Run("unversioned package returns default library URL", func(t *testing.T) {
		setupTestLibrary(t, `[
			{"name": "write-good", "url": "https://github.com/errata-ai/write-good"},
			{"name": "Google", "url": "https://github.com/errata-ai/Google"}
		]`)

		url := inLibrary("write-good", http.DefaultClient).URL
		if url != "https://github.com/errata-ai/write-good" {
			t.Errorf("expected 'https://github.com/errata-ai/write-good', got %q", url)
		}
	})

	t.Run("versioned package resolves matching release URL", func(t *testing.T) {
		setupTestLibrary(t, defaultGoogleLib)

		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			body := `[
				{"url": "https://api.github.com/repos/errata-ai/Google/releases/1", "zipball_url": "https://api.github.com/repos/errata-ai/Google/zipball/v0.1.0", "id": 1, "tag_name": "v0.1.0"},
				{"url": "https://api.github.com/repos/errata-ai/Google/releases/2", "zipball_url": "https://api.github.com/repos/errata-ai/Google/zipball/v0.2.0", "id": 2, "tag_name": "v0.2.0"}
			]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})

		url := inLibrary("Google@^0.1.0", client).URL
		expected := "https://api.github.com/repos/errata-ai/Google/zipball/v0.1.0"
		if url != expected {
			t.Errorf("expected %q, got %q", expected, url)
		}
	})

	t.Run("package not in library returns empty string", func(t *testing.T) {
		setupTestLibrary(t, defaultGoogleLib)

		url := inLibrary("NonExistentPackage", http.DefaultClient).URL
		if url != "" {
			t.Errorf("expected empty string for unknown package, got %q", url)
		}
	})

	t.Run("invalid semver constraint returns empty string", func(t *testing.T) {
		setupTestLibrary(t, defaultGoogleLib)

		url := inLibrary("Google@not-a-valid-constraint", http.DefaultClient).URL
		if url != "" {
			t.Errorf("expected empty string for invalid constraint, got %q", url)
		}
	})

	t.Run("no matching version found returns empty string", func(t *testing.T) {
		setupTestLibrary(t, defaultGoogleLib)

		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			body := `[{"url": "https://github.com/errata-ai/Google/releases/tag/v0.1.0", "id": 1, "tag_name": "v0.1.0"}]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})

		url := inLibrary("Google@>=1.0.0", client).URL
		if url != "" {
			t.Errorf("expected empty string when no version matches constraint, got %q", url)
		}
	})

	t.Run("asset selection prioritizes exact package name zip", func(t *testing.T) {
		setupTestLibrary(t, defaultGoogleLib)

		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			body := `[
				{
					"url": "https://github.com/errata-ai/Google/releases/tag/v0.1.0",
					"id": 1,
					"tag_name": "v0.1.0",
					"assets": [
						{"name": "Google-darwin.zip", "browser_download_url": "https://example.com/Google-darwin.zip"},
						{"name": "Google.zip", "browser_download_url": "https://example.com/Google.zip"}
					]
				}
			]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})

		url := inLibrary("Google@^0.1.0", client).URL
		expected := "https://example.com/Google.zip"
		if url != expected {
			t.Errorf("expected %q, got %q", expected, url)
		}
	})

	t.Run("asset selection falls back to any zip asset", func(t *testing.T) {
		setupTestLibrary(t, defaultGoogleLib)

		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			body := `[
				{
					"url": "https://github.com/errata-ai/Google/releases/tag/v0.1.0",
					"id": 1,
					"tag_name": "v0.1.0",
					"assets": [
						{"name": "Google-custom.zip", "browser_download_url": "https://example.com/Google-custom.zip"}
					]
				}
			]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})

		url := inLibrary("Google@^0.1.0", client).URL
		expected := "https://example.com/Google-custom.zip"
		if url != expected {
			t.Errorf("expected %q, got %q", expected, url)
		}
	})
}
