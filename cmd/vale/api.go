package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/pflag"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/system"
)

// Style represents an externally-hosted style.
type Style struct {
	// User-provided fields.
	Author      string `json:"author"`
	Description string `json:"description"`
	Deps        string `json:"deps"`
	Feed        string `json:"feed"`
	Homepage    string `json:"homepage"`
	Name        string `json:"name"`
	URL         string `json:"url"`

	// Generated fields.
	HasUpdate bool `json:"has_update"`
	InLibrary bool `json:"in_library"`
	Installed bool `json:"installed"`
	Addon     bool `json:"addon"`
}

// Meta represents an installed style's meta data.
type Meta struct {
	Author      string   `json:"author"`
	Coverage    float64  `json:"coverage"`
	Description string   `json:"description"`
	Email       string   `json:"email"`
	Feed        string   `json:"feed"`
	Lang        string   `json:"lang"`
	License     string   `json:"license"`
	Name        string   `json:"name"`
	Sources     []string `json:"sources"`
	URL         string   `json:"url"`
	Vale        string   `json:"vale_version"`
	Version     string   `json:"version"`
}

func init() {
	pflag.BoolVar(&Flags.Remote, "mode-rev-compat", false,
		"Prioritize remote Vale configurations.")
	pflag.StringVar(&Flags.Built, "built", "", "A post-processed file path.")

	commands["install"] = command{
		Run:     install,
		Summary: "Install a package from a URL.",
		Hidden:  true,
	}
}

func fetch(src, dst, cacheLoc string) error {
	if (cacheLoc != "") && system.FileExists(cacheLoc) {
		if err := system.Unarchive(cacheLoc, dst); err == nil {
			return nil
		}
		if err := os.Remove(cacheLoc); err != nil {
			return fmt.Errorf("removing invalid cache file: %w", err)
		}
	}
	// Fetch the resource from the web:
	resp, err := http.Get(src) //nolint:gosec,noctx

	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("could not fetch '%s' (status code '%d')", src, resp.StatusCode)
	}

	// Create a temp file to represent the archive locally:
	tmpfile, err := os.CreateTemp("", "temp.*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmpfile.Name()) // clean up
	defer tmpfile.Close()
	_, err = io.Copy(tmpfile, resp.Body)
	if err != nil {
		return err
	} else if err = tmpfile.Close(); err != nil {
		return err
	}

	if cacheLoc != "" {
		cacheFile, cacheErr := os.CreateTemp(filepath.Dir(cacheLoc), ".release-*.tmp")
		if cacheErr != nil {
			return cacheErr
		}
		cacheTmp := cacheFile.Name()
		defer os.Remove(cacheTmp)

		archive, openErr := os.Open(tmpfile.Name())
		if openErr != nil {
			cacheFile.Close()
			return openErr
		}

		if _, err = io.Copy(cacheFile, archive); err != nil {
			archive.Close()
			cacheFile.Close()
			return err
		}
		if err = archive.Close(); err != nil {
			cacheFile.Close()
			return err
		}
		if err = cacheFile.Close(); err != nil {
			return err
		}
		if err = os.Rename(cacheTmp, cacheLoc); err != nil {
			return err
		}
	}

	return system.Unarchive(tmpfile.Name(), dst)
}

func install(args []string, flags *core.CLIFlags) error {
	cfg, err := core.ReadPipeline(flags, false)
	if err != nil {
		return err
	}

	style := filepath.Join(cfg.StylesPath(), args[0])
	if system.IsDir(style) {
		os.RemoveAll(style) // Remove existing version
	}

	err = fetch(args[1], cfg.StylesPath(), "")
	if err != nil {
		return sendResponse(
			fmt.Sprintf("Failed to install '%s'", args[1]),
			err)
	}

	return sendResponse(fmt.Sprintf(
		"Successfully installed '%s'", args[1]), nil)
}
