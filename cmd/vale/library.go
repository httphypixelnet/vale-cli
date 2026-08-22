package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

type Release struct {
	URL             string          `json:"url"`
	ID              int             `json:"id"`
	TagName         *semver.Version `json:"tag_name"`
	TargetCommitish string          `json:"target_commitish"`
	Name            string          `json:"name"`
	Body            string          `json:"body"`
	Draft           bool            `json:"draft"`
	Prerelease      bool            `json:"prerelease"`
	Immutable       bool            `json:"immutable"`
	CreatedAt       time.Time       `json:"created_at"`
	PublishedAt     time.Time       `json:"published_at"`
	Assets          []Asset         `json:"assets"`
}

type Asset struct {
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ID                 int    `json:"id"`
	NodeID             string `json:"node_id"`
	Name               string `json:"name"`
	Label              string `json:"label"`
	State              string `json:"state"`
	ContentType        string `json:"content_type"`
	Size               int    `json:"size"`
	Digest             string `json:"digest"`
}

func (r *Release) UnmarshalJSON(data []byte) error {
	type Alias Release

	var aux struct {
		*Alias
		TagName string `json:"tag_name"`
	}

	aux.Alias = (*Alias)(r)

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if aux.TagName != "" {
		version, err := semver.NewVersion(aux.TagName)
		if err == nil {
			r.TagName = version
		} else {
			r.TagName = nil
		}
	} else {
		r.TagName = nil
	}

	return nil
}

var repoPattern = regexp.MustCompile(`^https?://github\.com/(?P<owner>[^/]+)/(?P<repo>[^/]+)`)
var library = "https://raw.githubusercontent.com/vale-cli/packages/master/library.json"

func getLibrary() ([]Style, error) {
	styles := []Style{}

	resp, err := fetchJSON(library)
	if err != nil {
		return styles, err
	} else if err = json.Unmarshal(resp, &styles); err != nil {
		return styles, err
	}

	return styles, err
}

func lookupReleases(repoInfo RepoParseResult, cache string, client *http.Client) ([]Release, error) {
	var releases []Release
	const cacheTTL = 15 * time.Minute
	var cacheFile = filepath.Join(cache, repoInfo.owner+"."+repoInfo.repo+".json")
	if fresh, err := isFresh(cacheFile, cacheTTL); err == nil && fresh {
		bytes, readErr := os.ReadFile(cacheFile)
		if readErr == nil {
			if unmarshalErr := json.Unmarshal(bytes, &releases); unmarshalErr == nil {
				return releases, nil
			}
		}
	}
	releases, err := paginate[Release](fmt.Sprintf("/repos/%s/%s/releases", repoInfo.owner, repoInfo.repo), client)
	if err != nil {
		return releases, err
	}
	text, err := json.Marshal(releases)
	if err != nil {
		return releases, err
	}
	err = os.WriteFile(cacheFile, text, 0600)
	return releases, err
}
func cacheDir() (string, error) {
	var final string
	if cache := os.Getenv("VALE_CACHE"); cache != "" {
		final = cache
	} else {
		switch runtime.GOOS {
		case "windows":
			base := os.Getenv("LOCALAPPDATA")
			if base == "" {
				return "", errors.New("LOCALAPPDATA is not set")
			}
			final = filepath.Join(base, "vale-cli", "Cache")
		case "darwin":
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			final = filepath.Join(home, "Library", "Caches", "vale-cli")

		default:
			if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
				final = filepath.Join(xdg, "vale-cli")
				break
			}

			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			final = filepath.Join(home, ".cache", "vale-cli")
		}
	}

	final = filepath.Clean(final)
	if err := os.MkdirAll(final, 0700); err != nil {
		return "", err
	}
	return final, nil
}

func getMatchingRelease(repoInfo RepoParseResult, tag *semver.Constraints, client *http.Client) (*Release, error) {
	cache, cacheErr := cacheDir()
	if cacheErr != nil {
		return nil, cacheErr
	}

	releases, releasesErr := lookupReleases(repoInfo, cache, client)
	if releasesErr != nil {
		return nil, releasesErr
	}

	var validReleases []Release
	for _, release := range releases {
		if release.TagName != nil && tag.Check(release.TagName) {
			validReleases = append(validReleases, release)
		}
	}

	if len(validReleases) == 0 {
		return nil, errors.New("no matching release found")
	}
	sort.Slice(validReleases, func(i, j int) bool {
		return validReleases[i].TagName.LessThan(validReleases[j].TagName)
	})
	return &validReleases[len(validReleases)-1], nil
}

type RepoParseResult struct {
	repo  string
	owner string
}

func parseRepo(url string) (RepoParseResult, error) {
	var result = RepoParseResult{}
	match := repoPattern.FindStringSubmatch(url)
	if (len(match)) != 3 {
		return result, errors.New("unable to parse input url")
	}
	result.owner = match[1]
	result.repo = match[2]
	return result, nil
}

func inLibrary(pkg string, client *http.Client) string {
	lookup, err := getLibrary()
	if err != nil {
		return ""
	}
	name, rawVersion, hasVersion := strings.Cut(pkg, "@")
	var version *semver.Constraints
	if hasVersion {
		version, err = semver.NewConstraint(rawVersion)
		if err != nil {
			return ""
		}
	}

	for _, entry := range lookup {
		if name == entry.Name {
			if version == nil {
				return entry.URL
			}
			parsed, parseErr := parseRepo(entry.URL)
			if parseErr != nil {
				return ""
			}
			release, matchErr := getMatchingRelease(parsed, version, client)
			if matchErr != nil {
				return ""
			}
			for _, asset := range release.Assets {
				if strings.EqualFold(asset.Name, name+".zip") {
					return asset.BrowserDownloadURL
				}
			}
			for _, asset := range release.Assets {
				if strings.HasSuffix(asset.Name, ".zip") {
					return asset.BrowserDownloadURL
				}
			}
			if len(release.Assets) > 0 {
				return release.Assets[0].BrowserDownloadURL
			}
			return release.URL
		}
	}
	return ""
}
