package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/system"
)

func setupLocalSyncTestPackage(t *testing.T, root string) (string, string) {
	t.Helper()

	stylesPath := filepath.Join(root, "missing-styles")
	cfgPath := filepath.Join(root, ".vale.ini")
	pkgRoot := filepath.Join(root, "local-package")
	pkgStyles := filepath.Join(pkgRoot, "styles", "TestStyle")

	if err := os.MkdirAll(pkgStyles, os.ModePerm); err != nil {
		t.Fatalf("Failed to create local package styles directory %q: %v", pkgStyles, err)
	}

	rulePath := filepath.Join(pkgStyles, "Rule.yml")
	if err := os.WriteFile(rulePath, []byte("extends: existence\nmessage: test\nlevel: warning\n"), 0o600); err != nil {
		t.Fatalf("Failed to write local package rule file %q: %v", rulePath, err)
	}

	body := []byte("StylesPath = missing-styles\nPackages = " + pkgRoot + "\n")
	if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
		t.Fatalf("Failed to write test config file %q: %v", cfgPath, err)
	}

	return stylesPath, cfgPath
}

func TestSyncCreatesConfiguredMissingStylesPath(t *testing.T) {
	root := t.TempDir()
	stylesPath, cfgPath := setupLocalSyncTestPackage(t, root)

	if system.IsDir(stylesPath) {
		t.Fatalf("Expected StylesPath to be missing before sync: %s", stylesPath)
	}

	err := sync(nil, &core.CLIFlags{
		Path:         cfgPath,
		IgnoreGlobal: true,
	})
	if err != nil {
		t.Fatalf("Command 'sync' failed while creating configured StylesPath %q from config %q: %v", stylesPath, cfgPath, err)
	}

	if !system.IsDir(stylesPath) {
		t.Fatalf("Expected sync to create configured StylesPath: %s", stylesPath)
	}
}

func TestSyncInstallsPackageIntoConfiguredStylesPath(t *testing.T) {
	root := t.TempDir()
	stylesPath, cfgPath := setupLocalSyncTestPackage(t, root)

	err := sync(nil, &core.CLIFlags{
		Path:         cfgPath,
		IgnoreGlobal: true,
	})
	if err != nil {
		t.Fatalf("Command 'sync' failed while installing local package into configured StylesPath %q: %v", stylesPath, err)
	}

	installedRule := filepath.Join(stylesPath, "TestStyle", "Rule.yml")
	if !system.FileExists(installedRule) {
		t.Fatalf("Expected package asset to be installed into configured StylesPath: %s", installedRule)
	}
}

func TestSyncDoesNotInstallPackageIntoGlobalStylesPath(t *testing.T) {
	root := t.TempDir()
	globalStylesPath := filepath.Join(root, "global-styles")
	stylesPath, cfgPath := setupLocalSyncTestPackage(t, root)

	if err := os.MkdirAll(globalStylesPath, os.ModePerm); err != nil {
		t.Fatalf("Failed to create global StylesPath %q: %v", globalStylesPath, err)
	}
	t.Setenv("VALE_STYLES_PATH", globalStylesPath)

	err := sync(nil, &core.CLIFlags{
		Path: cfgPath,
	})
	if err != nil {
		t.Fatalf("Command 'sync' failed with configured StylesPath %q and global StylesPath %q: %v", stylesPath, globalStylesPath, err)
	}

	installedRule := filepath.Join(stylesPath, "TestStyle", "Rule.yml")
	if !system.FileExists(installedRule) {
		t.Fatalf("Expected package asset to be installed into configured StylesPath: %s", installedRule)
	}

	wrongGlobalRule := filepath.Join(globalStylesPath, "TestStyle", "Rule.yml")
	if system.FileExists(wrongGlobalRule) {
		t.Fatalf("Expected package asset not to be installed into global StylesPath: %s", wrongGlobalRule)
	}
}

func TestSyncPreservesLocalPackageINI(t *testing.T) {
	// A local directory package's own .vale.ini must not be renamed/moved out
	// of the user's source directory during sync. See #991 (regression of
	// #583).
	root := t.TempDir()
	pkgRoot := filepath.Join(root, "local-package")
	pkgStyles := filepath.Join(pkgRoot, "styles", "TestStyle")
	if err := os.MkdirAll(pkgStyles, os.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgStyles, "Rule.yml"),
		[]byte("extends: existence\nmessage: test\nlevel: warning\ntokens: [foo]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pkgINI := filepath.Join(pkgRoot, ".vale.ini")
	if err := os.WriteFile(pkgINI,
		[]byte("StylesPath = styles\n[*]\nBasedOnStyles = TestStyle\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgPath := filepath.Join(root, ".vale.ini")
	if err := os.WriteFile(cfgPath,
		[]byte("StylesPath = missing-styles\nPackages = "+pkgRoot+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := sync(nil, &core.CLIFlags{Path: cfgPath, IgnoreGlobal: true}); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if !system.FileExists(pkgINI) {
		t.Fatalf("sync renamed/removed the local package's .vale.ini: %s", pkgINI)
	}

	// The config should still have been installed into the pipeline directory.
	installed := filepath.Join(root, "missing-styles", core.PipeDir, "0-local-package.ini")
	if !system.FileExists(installed) {
		t.Fatalf("expected package config in the pipeline directory: %s", installed)
	}
}

func TestSyncDoesNotInstallPackageIntoConfigRoot(t *testing.T) {
	root := t.TempDir()
	_, cfgPath := setupLocalSyncTestPackage(t, root)

	err := sync(nil, &core.CLIFlags{
		Path:         cfgPath,
		IgnoreGlobal: true,
	})
	if err != nil {
		t.Fatalf("Command 'sync' failed while checking that package is not installed into config root %q: %v", root, err)
	}

	wrongConfigRootRule := filepath.Join(root, "TestStyle", "Rule.yml")
	if system.FileExists(wrongConfigRootRule) {
		t.Fatalf("Expected package asset not to be installed into config root: %s", wrongConfigRootRule)
	}
}

func TestSyncVersionedPackage(t *testing.T) {
	packages := []string{"Google@0.7.0", "Google@^0.7.0"}
	for _, pkg := range packages {
		t.Run(pkg, func(t *testing.T) {
			root := t.TempDir()
			stylesPath := filepath.Join(root, "styles")
			cfgPath := filepath.Join(root, ".vale.ini")

			body := []byte(fmt.Sprintf("StylesPath = styles\nPackages = %s\n", pkg))
			if err := os.WriteFile(cfgPath, body, 0o600); err != nil {
				t.Fatalf("Failed to write test config file: %v", err)
			}

			err := sync(nil, &core.CLIFlags{
				Path:         cfgPath,
				IgnoreGlobal: true,
			})
			if err != nil {
				t.Fatalf("Command 'sync' failed for package %q: %v", pkg, err)
			}

			installedDir := filepath.Join(stylesPath, "Google")
			if !system.IsDir(installedDir) {
				t.Fatalf("Expected package directory %q to be installed in StylesPath", installedDir)
			}

			installedRule := filepath.Join(installedDir, "Headings.yml")
			if !system.FileExists(installedRule) {
				t.Fatalf("Expected rule file %q to exist in installed package", installedRule)
			}
		})
	}
}

func TestIsFresh(t *testing.T) {
	t.Run("non-existent file returns false and nil error", func(t *testing.T) {
		tempDir := t.TempDir()
		nonExistent := filepath.Join(tempDir, "does-not-exist.json")

		fresh, err := isFresh(nonExistent, 10*time.Minute)
		if err != nil {
			t.Fatalf("unexpected error for non-existent file: %v", err)
		}
		if fresh {
			t.Error("expected non-existent file not to be fresh")
		}
	})

	t.Run("fresh file returns true and nil error", func(t *testing.T) {
		tempDir := t.TempDir()
		freshFile := filepath.Join(tempDir, "fresh.json")
		if err := os.WriteFile(freshFile, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}

		fresh, err := isFresh(freshFile, 10*time.Minute)
		if err != nil {
			t.Fatalf("unexpected error for fresh file: %v", err)
		}
		if !fresh {
			t.Error("expected recently modified file to be fresh")
		}
	})

	t.Run("stale file returns false and nil error", func(t *testing.T) {
		tempDir := t.TempDir()
		staleFile := filepath.Join(tempDir, "stale.json")
		if err := os.WriteFile(staleFile, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}

		pastTime := time.Now().Add(-20 * time.Minute)
		if err := os.Chtimes(staleFile, pastTime, pastTime); err != nil {
			t.Fatal(err)
		}

		fresh, err := isFresh(staleFile, 10*time.Minute)
		if err != nil {
			t.Fatalf("unexpected error for stale file: %v", err)
		}
		if fresh {
			t.Error("expected old file not to be fresh")
		}
	})

	t.Run("stat error on invalid path component returns error", func(t *testing.T) {
		tempDir := t.TempDir()
		regularFile := filepath.Join(tempDir, "regular-file")
		if err := os.WriteFile(regularFile, []byte("content"), 0o600); err != nil {
			t.Fatal(err)
		}

		invalidPath := filepath.Join(regularFile, "subfile.json")
		fresh, err := isFresh(invalidPath, 10*time.Minute)
		if err == nil {
			t.Fatalf("expected stat error for invalid path, got fresh=%v, err=nil", fresh)
		}
		if fresh {
			t.Error("expected fresh to be false on error")
		}
	})
}

type testItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func TestPaginate(t *testing.T) {
	t.Run("single page without link header", func(t *testing.T) {
		var receivedReq *http.Request
		client := newMockHTTPClient(func(req *http.Request) (*http.Response, error) {
			receivedReq = req
			body := `[{"id": 1, "name": "Item 1"}, {"id": 2, "name": "Item 2"}]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})

		items, err := paginate[testItem]("/test/items", client)
		if err != nil {
			t.Fatalf("unexpected error from paginate: %v", err)
		}

		if len(items) != 2 {
			t.Fatalf("expected 2 items, got %d", len(items))
		}
		if items[0].Name != "Item 1" || items[1].Name != "Item 2" {
			t.Errorf("unexpected items content: %+v", items)
		}

		if receivedReq == nil {
			t.Fatal("expected request to be received")
		}
		if receivedReq.Header.Get("Accept") != "application/vnd.github.v3+json" {
			t.Errorf("expected Accept header 'application/vnd.github.v3+json', got %q", receivedReq.Header.Get("Accept"))
		}
		if receivedReq.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
			t.Errorf("expected X-GitHub-Api-Version header '2026-03-10', got %q", receivedReq.Header.Get("X-GitHub-Api-Version"))
		}
		if receivedReq.URL.Query().Get("per_page") != "100" {
			t.Errorf("expected per_page=100 query param by default, got %q", receivedReq.URL.Query().Get("per_page"))
		}
	})

	t.Run("preserves existing per_page query param", func(t *testing.T) {
		var receivedReq *http.Request
		client := newMockHTTPClient(func(req *http.Request) (*http.Response, error) {
			receivedReq = req
			body := `[{"id": 1, "name": "Item 1"}]`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})

		items, err := paginate[testItem]("/test/items?per_page=50", client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(items))
		}
		if receivedReq.URL.Query().Get("per_page") != "50" {
			t.Errorf("expected per_page=50 to be preserved, got %q", receivedReq.URL.Query().Get("per_page"))
		}
	})

	t.Run("multiple pages following next link", func(t *testing.T) {
		pageCount := 0
		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			pageCount++
			header := make(http.Header)
			var body string

			switch pageCount {
			case 1:
				header.Set("Link", `<https://api.github.com/test/items?page=2>; rel="next", <https://api.github.com/test/items?page=3>; rel="last"`)
				body = `[{"id": 1, "name": "Page 1 Item"}]`
			case 2:
				header.Set("Link", `<https://api.github.com/test/items?page=3>; rel="next"`)
				body = `[{"id": 2, "name": "Page 2 Item"}]`
			case 3:
				body = `[{"id": 3, "name": "Page 3 Item"}]`
			default:
				return nil, fmt.Errorf("unexpected request count: %d", pageCount)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     header,
			}, nil
		})

		items, err := paginate[testItem]("/test/items", client)
		if err != nil {
			t.Fatalf("unexpected error from paginate: %v", err)
		}
		if pageCount != 3 {
			t.Errorf("expected 3 pages requested, got %d", pageCount)
		}
		if len(items) != 3 {
			t.Fatalf("expected 3 items total, got %d", len(items))
		}
		if items[0].Name != "Page 1 Item" || items[1].Name != "Page 2 Item" || items[2].Name != "Page 3 Item" {
			t.Errorf("unexpected items content: %+v", items)
		}
	})

	t.Run("non-200 HTTP response returns error", func(t *testing.T) {
		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Status:     "404 Not Found",
				Body:       io.NopCloser(strings.NewReader(`{"message": "Not Found"}`)),
				Header:     make(http.Header),
			}, nil
		})

		_, err := paginate[testItem]("/test/notfound", client)
		if err == nil {
			t.Fatal("expected error on 404 response, got nil")
		}
		if !strings.Contains(err.Error(), "unexpected status 404") {
			t.Errorf("expected error message to contain 'unexpected status 404', got %q", err.Error())
		}
	})

	t.Run("invalid json response returns error", func(t *testing.T) {
		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Body:       io.NopCloser(strings.NewReader(`invalid json content`)),
				Header:     make(http.Header),
			}, nil
		})

		_, err := paginate[testItem]("/test/items", client)
		if err == nil {
			t.Fatal("expected decode error on malformed json, got nil")
		}
	})

	t.Run("transport network error returns error", func(t *testing.T) {
		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("connection reset by peer")
		})

		_, err := paginate[testItem]("/test/items", client)
		if err == nil {
			t.Fatal("expected transport error, got nil")
		}
	})

	t.Run("invalid url path returns error", func(t *testing.T) {
		client := newMockHTTPClient(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("unexpected client call")
		})

		_, err := paginate[testItem]("://invalid-url\x7f", client)
		if err == nil {
			t.Fatal("expected parse error on invalid url path, got nil")
		}
	})
}
