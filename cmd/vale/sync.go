package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	cp "github.com/otiai10/copy"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/system"
)

func initPath(cfg *core.Config) error {
	// The first entry is always the default `StylesPath`.
	stylesPath := cfg.StylesPath()

	if !system.IsDir(stylesPath) {
		if err := os.MkdirAll(stylesPath, os.ModePerm); err != nil {
			e := fmt.Errorf("unable to initialize StylesPath (value = '%s')", stylesPath)
			return core.NewE100("initPath", e)
		}
	}

	// Remove any existing .vale-config directory.
	err := os.RemoveAll(filepath.Join(stylesPath, core.PipeDir))
	if err != nil {
		return core.NewE100("initPath", err)
	}

	return nil
}

func readPkg(pkg, path string, idx int) error {
	if !system.IsDir(pkg) {
		entry := inLibrary(pkg, http.DefaultClient)
		if entry != "" {
			name, _, _ := strings.Cut(pkg, "@")
			return download(name, entry, path, idx)
		}
	}
	return loadPkg(system.FileNameWithoutExt(pkg), pkg, path, idx)
}

func isFresh(path string, ttl time.Duration) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}

	return time.Since(info.ModTime()) < ttl, nil
}

var nextPattern = regexp.MustCompile(`(?i)<([^>]+)>;\s*rel="next"`)

func paginate[T any](rawURL string, client *http.Client) ([]T, error) {
	var result []T
	parsedURL, err := url.Parse("https://api.github.com" + rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	q := parsedURL.Query()
	if q.Get("per_page") == "" {
		q.Set("per_page", "100")
	}
	parsedURL.RawQuery = q.Encode()
	nextURL := parsedURL.String()

	for nextURL != "" {
		req, reqErr := http.NewRequest(http.MethodGet, nextURL, nil)
		if reqErr != nil {
			return result, reqErr
		}
		req.Header.Add("Accept", "application/vnd.github.v3+json")
		req.Header.Add("X-GitHub-Api-Version", "2026-03-10")

		res, doErr := client.Do(req)
		if doErr != nil {
			return result, doErr
		}
		readErr := func() error {
			defer res.Body.Close()

			if res.StatusCode != http.StatusOK {
				return fmt.Errorf("unexpected status %d: %s", res.StatusCode, res.Status)
			}

			var page []T
			if decodeErr := json.NewDecoder(res.Body).Decode(&page); decodeErr != nil {
				return decodeErr
			}

			result = append(result, page...)
			return nil
		}()
		if readErr != nil {
			return result, readErr
		}
		linkHeader := res.Header.Get("Link")
		matches := nextPattern.FindStringSubmatch(linkHeader)
		if len(matches) > 1 {
			nextURL = matches[1]
		} else {
			nextURL = ""
		}
	}
	return result, nil
}

func loadPkg(name, urlOrPath, styles string, index int) error {
	if fileInfo, err := os.Stat(urlOrPath); err == nil {
		if fileInfo.IsDir() {
			return loadLocalPkg(name, urlOrPath, styles, index)
		}
		return loadLocalZipPkg(name, urlOrPath, styles, index)
	}
	return download(name, urlOrPath, styles, index)
}

func loadLocalPkg(name, pkgPath, styles string, index int) error {
	return installPkg(filepath.Dir(pkgPath), name, styles, index)
}

func loadLocalZipPkg(name, pkgPath, styles string, index int) error {
	dir, err := os.MkdirTemp("", name)
	if err != nil {
		return err
	}

	if err = system.Unarchive(pkgPath, dir); err != nil {
		return err
	}

	return installPkg(dir, name, styles, index)
}

func download(name, url, styles string, index int) error {
	dir, err := os.MkdirTemp("", name)
	if err != nil {
		return err
	}

	if err = fetch(url, dir); err != nil {
		if strings.Contains(err.Error(), "unsupported protocol scheme") {
			err = fmt.Errorf("'%s' is not a valid URL or the directory doesn't exist", url)
		}
		return core.NewE100("download", err)
	}

	return installPkg(dir, name, styles, index)
}

func installPkg(dir, name, styles string, index int) error {
	root := filepath.Join(dir, name)
	path := filepath.Join(root, "styles")
	pipe := filepath.Join(styles, core.PipeDir)
	cfg := filepath.Join(root, ".vale.ini")

	if !system.IsDir(path) && !system.FileExists(cfg) {
		return moveAsset(name, dir, styles) // style-only
	}

	// StylesPath
	if system.IsDir(path) {
		if err := moveDir(path, styles); err != nil {
			return err
		}
		// $StylesPath/config
		//
		// NOTE: We treat this directory differently than the rest of the
		// entries on the path: we merge its contents with the existing
		// $StylesPath/config directory.
		for _, dir := range core.ConfigDirs {
			loc1 := filepath.Join(path, dir)
			if system.IsDir(loc1) {
				loc2 := filepath.Join(styles, dir)
				if err := moveDir(loc1, loc2); err != nil {
					return err
				}
			}
		}
	}

	// .vale.ini
	if system.FileExists(cfg) {
		pkgs, err := core.GetPackages(cfg)
		if err != nil {
			return err
		}

		for idx, pkg := range pkgs {
			if err = readPkg(pkg, styles, idx); err != nil {
				return err
			}
		}
		// Copy the package's .vale.ini into the pipeline directory under its
		// indexed name. We must not rename it in place: for a local directory
		// package, `root` is the user's actual source directory, so renaming
		// would clobber their original .vale.ini. See #991 (and #583).
		dst := filepath.Join(pipe, fmt.Sprintf("%d-%s.ini", index, name))
		if system.FileExists(dst) {
			if err = os.RemoveAll(dst); err != nil {
				return err
			}
		}
		if err = os.MkdirAll(pipe, os.ModePerm); err != nil {
			return err
		}
		if err = cp.Copy(cfg, dst); err != nil {
			return err
		}
	}

	return nil
}

func moveDir(oldPath, newPath string) error {
	files, err := os.ReadDir(oldPath)
	if err != nil {
		return err
	}

	for _, file := range files {
		if !file.IsDir() || file.Name() != "config" {
			if err = moveAsset(file.Name(), oldPath, newPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func moveAsset(name, oldPath, newPath string) error {
	src := filepath.Join(oldPath, name)
	dst := filepath.Join(newPath, name)

	if system.FileExists(dst) || system.IsDir(dst) {
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}

	err := os.MkdirAll(newPath, os.ModePerm)
	if err != nil {
		return err
	}

	return cp.Copy(src, dst)
}
