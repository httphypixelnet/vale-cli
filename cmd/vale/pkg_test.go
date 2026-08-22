package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/system"
)

var TestData = "../../testdata/pkg"

func mockPath() (string, error) {
	cfg, err := core.NewConfig(&core.CLIFlags{})
	if err != nil {
		return "", err
	}
	cfg.AddStylesPath(os.TempDir())

	err = initPath(cfg)
	if err != nil {
		return "", err
	}

	return cfg.StylesPath(), nil
}

func TestNoPkgFound(t *testing.T) {
	path, err := mockPath()
	if err != nil {
		t.Fatal(err)
	}

	err = readPkg("https://github.com/errata-ai/Microsoft/releases/download/v0.14.x/Microsoft.zip", path, 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	msg := "could not fetch 'https://github.com/errata-ai/Microsoft/releases/download/v0.14.x/Microsoft.zip' (status code '404')"
	if !strings.Contains(err.Error(), msg) {
		t.Fatalf("expected '%s', got '%s'", msg, err.Error())
	}
}

func TestLibrary(t *testing.T) {
	path, err := mockPath()
	if err != nil {
		t.Fatal(err)
	}

	err = readPkg("write-good", path, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !system.IsDir(filepath.Join(path, "write-good")) {
		t.Fatal("unable to find 'write-good' in StylesPath")
	}

	if !system.FileExists(filepath.Join(path, "write-good", "E-Prime.yml")) {
		t.Fatal("unable to find 'E-Prime' in StylesPath")
	}
}

func TestVersionedLibrary(t *testing.T) {
	packages := []string{"Google@0.7.0", "Google@^0.7.0"}
	for _, pkg := range packages {
		t.Run(pkg, func(t *testing.T) {
			path, err := mockPath()
			if err != nil {
				t.Fatal(err)
			}

			err = readPkg(pkg, path, 0)
			if err != nil {
				t.Fatalf("failed to read package %q: %v", pkg, err)
			}

			if !system.IsDir(filepath.Join(path, "Google")) {
				t.Fatalf("unable to find 'Google' in StylesPath for %q", pkg)
			}

			if !system.FileExists(filepath.Join(path, "Google", "Headings.yml")) {
				t.Fatalf("unable to find 'Headings.yml' in StylesPath for %q", pkg)
			}
		})
	}
}

func TestLocalZip(t *testing.T) {
	path, err := mockPath()
	if err != nil {
		t.Fatal(err)
	}

	zip, err := filepath.Abs(filepath.Join(TestData, "write-good.zip"))
	if err != nil {
		t.Fatal(err)
	}

	err = readPkg(zip, path, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !system.IsDir(filepath.Join(path, "write-good")) {
		t.Fatal("unable to find 'write-good' in StylesPath")
	}

	if !system.FileExists(filepath.Join(path, "write-good", "E-Prime.yml")) {
		t.Fatal("unable to find 'E-Prime' in StylesPath")
	}
}

func TestLocalDir(t *testing.T) {
	path, err := mockPath()
	if err != nil {
		t.Fatal(err)
	}

	zip, err := filepath.Abs(filepath.Join(TestData, "write-good"))
	if err != nil {
		t.Fatal(err)
	}

	err = readPkg(zip, path, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !system.IsDir(filepath.Join(path, "write-good")) {
		t.Fatal("unable to find 'write-good' in StylesPath")
	}

	if !system.FileExists(filepath.Join(path, "write-good", "E-Prime.yml")) {
		t.Fatal("unable to find 'E-Prime' in StylesPath")
	}
}

func TestLocalComplete(t *testing.T) { //nolint:dupl
	path, err := mockPath()
	if err != nil {
		t.Fatal(err)
	}

	zip, err := filepath.Abs(filepath.Join(TestData, "ISC.zip"))
	if err != nil {
		t.Fatal(err)
	}

	err = readPkg(zip, path, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !system.IsDir(filepath.Join(path, "ISC")) {
		t.Fatal("unable to find 'ISC' in StylesPath")
	}

	vocab := filepath.Join(path, "Vocab", "ISC_General", "accept.txt")
	if !system.FileExists(vocab) {
		t.Fatal("unable to find 'ISC_General' in Vocab")
	}

	b, err := os.ReadFile(vocab)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")

	if !core.StringInSlice("bar", lines) {
		t.Fatalf("unable to find 'bar' in %v", lines)
	}
}

func TestLocalOnlyStyles(t *testing.T) { //nolint:dupl
	path, err := mockPath()
	if err != nil {
		t.Fatal(err)
	}

	zip, err := filepath.Abs(filepath.Join(TestData, "OnlyStyles.zip"))
	if err != nil {
		t.Fatal(err)
	}

	err = readPkg(zip, path, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !system.IsDir(filepath.Join(path, "ISC")) {
		t.Fatal("unable to find 'ISC' in StylesPath")
	}

	vocab := filepath.Join(path, "Vocab", "ISC_General", "accept.txt")
	if !system.FileExists(vocab) {
		t.Fatal("unable to find 'ISC_General' in Vocab")
	}

	b, err := os.ReadFile(vocab)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")

	if !core.StringInSlice("bar", lines) {
		t.Fatalf("unable to find 'bar' in %v", lines)
	}
}

func TestV3Pkg(t *testing.T) {
	path, err := mockPath()
	if err != nil {
		t.Fatal(err)
	}

	zip, err := filepath.Abs(filepath.Join(TestData, "v3.zip"))
	if err != nil {
		t.Fatal(err)
	}

	err = readPkg(zip, path, 0)
	if err != nil {
		t.Fatal(err)
	}

	if !system.IsDir(filepath.Join(path, "config")) {
		t.Fatal("unable to find 'config' in StylesPath")
	}

	if !system.FileExists(filepath.Join(path, core.VocabDir, "Basic", "accept.txt")) {
		t.Fatal("unable to find 'accept.txt'")
	}

	if !system.FileExists(filepath.Join(path, core.TmplDir, "t.tmpl")) {
		t.Fatal("unable to find 't.tmpl'")
	}
}
