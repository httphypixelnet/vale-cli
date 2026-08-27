package main

import (
	"crypto/sha256"
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
		if entry.URL != "" {
			var cachePath, _ = getReleaseCachePath(entry.Version, entry.Name)
			return download(entry.Name, entry.URL, path, idx, cachePath)
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

func getPageCachePath(rawURL string) (string, error) {
	var hash = sha256.Sum256([]byte(rawURL))
	var pageFile, err = buildCachePath(CachePage, fmt.Sprintf("%x", hash[:]), "page.json")
	if err != nil {
		return "", err
	}
	return pageFile, nil
}

func getReleaseCachePath(version, name string) (string, error) {
	var releaseFile, err = buildCachePath(CacheRelease, fmt.Sprintf("%s@%s", name, version), "release.json")
	if err != nil {
		return "", err
	}
	return releaseFile, nil
}

type Cache int

const (
	Error Cache = iota
	CachePage
	CacheRelease
)

func buildCachePath(c Cache, path ...string) (string, error) {
	var cache, err = cacheDir()
	if err != nil {
		return "", err
	}
	var p string
	switch c {
	case CachePage:
		p, err = filepath.Join(append([]string{cache, "pages"}, path...)...), nil

	case CacheRelease:
		p, err = filepath.Join(append([]string{cache, "releases"}, path...)...), nil
	default:
		err = errors.New("invalid cache type")
	}
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(p), os.ModePerm); err != nil {
		return "", err
	}
	return p, nil
}

type Page[T any] struct {
	Items []T    `json:"items"`
	Etag  string `json:"etag"`
}

func getPageFromCache[T any](rawURl string) (Page[T], error) {
	var page Page[T]
	var path, err = getPageCachePath(rawURl)
	if err != nil {
		return page, err
	}
	var pageFile = filepath.Join(path)
	data, err := os.ReadFile(pageFile)
	if err != nil {
		return page, err
	}
	err = json.Unmarshal(data, &page)
	if err != nil {
		return page, err
	}
	return page, err
}

func paginate[T any](rawURL, etag string, client *http.Client) ([]T, error) {
	var result []T
	var isFirst = true
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
		req, err := http.NewRequest(http.MethodGet, nextURL, nil)
		if err != nil {
			return result, err
		}

		req.Header.Set("If-None-Match", etag)
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("X-GitHub-Api-Version", "2026-03-10")

		res, err := client.Do(req)
		if err != nil {
			return result, err
		}

		if res.StatusCode == http.StatusNotModified {
			page, err := getPageFromCache[T](rawURL)
			res.Body.Close()

			if err != nil {
				return paginate[T](rawURL, "", client)
			}

			result = append(result, page.Items...)
		} else {
			if res.StatusCode != http.StatusOK {
				res.Body.Close()
				return result, fmt.Errorf(
					"unexpected status %d: %s",
					res.StatusCode,
					res.Status,
				)
			}
			if isFirst {
				etag = res.Header.Get("ETag")
				isFirst = false
			}
			var page []T
			err := json.NewDecoder(res.Body).Decode(&page)
			res.Body.Close()

			if err != nil {
				return result, err
			}

			result = append(result, page...)
		}

		matches := nextPattern.FindStringSubmatch(res.Header.Get("Link"))
		if len(matches) > 1 {
			nextURL = matches[1]
		} else {
			nextURL = ""
		}
	}
	_ = cachePageResponse[T](result, rawURL, etag)
	return result, nil
}
func cachePageResponse[T any](data []T, rawURL, Etag string) error {
	path, err := getPageCachePath(rawURL)
	if err != nil {
		return err
	}
	pageFile := filepath.Join(path)
	err = os.MkdirAll(filepath.Dir(pageFile), os.ModePerm)
	if err != nil {
		return err
	}
	p := Page[T]{
		Items: data,
		Etag:  Etag,
	}
	dataBytes, err := json.Marshal(p)
	if err != nil {
		return err
	}

	err = os.WriteFile(pageFile, dataBytes, 0600)
	if err != nil {
		return err
	}
	return nil
}
func loadPkg(name, urlOrPath, styles string, index int) error {
	if fileInfo, err := os.Stat(urlOrPath); err == nil {
		if fileInfo.IsDir() {
			return loadLocalPkg(name, urlOrPath, styles, index)
		}
		return loadLocalZipPkg(name, urlOrPath, styles, index)
	}
	return download(name, urlOrPath, styles, index, "")
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

func download(name, url, styles string, index int, cacheLoc string) error {
	dir, err := os.MkdirTemp("", name)
	if err != nil {
		return err
	}

	if err = fetch(url, dir, cacheLoc); err != nil {
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
