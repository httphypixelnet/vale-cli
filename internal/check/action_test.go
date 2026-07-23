package check

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/errata-ai/vale/v3/internal/core"
)

const actionScript = `
suggestions := [prefix + "-" + match]
`

func writeActionScript(t *testing.T, path, prefix string) {
	t.Helper()

	err := os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte("prefix := \""+prefix+"\"\n"+actionScript), 0o644)
	if err != nil {
		t.Fatal(err)
	}
}

func actionTestConfig(t *testing.T, stylesPath string) *core.Config {
	t.Helper()

	cfg, err := core.NewConfig(&core.CLIFlags{IgnoreGlobal: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg.AddStylesPath(stylesPath)

	return cfg
}

func actionTestAlert(scriptName string) core.Alert {
	return core.Alert{
		Action: core.Action{
			Name:   "suggest",
			Params: []string{scriptName},
		},
		Check: "RedHat.NoGerundsInTitles",
		Match: "Running",
	}
}

func TestSuggestActionFindsScriptInStyleDirectory(t *testing.T) {
	stylesPath := t.TempDir()
	scriptName := "NoGerundsInTitles.tengo"
	writeActionScript(t, filepath.Join(stylesPath, "RedHat", scriptName), "style")

	got, err := FixAlert(actionTestAlert(scriptName), actionTestConfig(t, stylesPath))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"style-Running"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestSuggestActionPrefersConfigActionsScript(t *testing.T) {
	stylesPath := t.TempDir()
	scriptName := "NoGerundsInTitles.tengo"
	writeActionScript(t, filepath.Join(stylesPath, core.ActionDir, scriptName), "config")
	writeActionScript(t, filepath.Join(stylesPath, "RedHat", scriptName), "style")

	got, err := FixAlert(actionTestAlert(scriptName), actionTestConfig(t, stylesPath))
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"config-Running"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestSuggestActionRequiresStyleQualifiedCheckForFallback(t *testing.T) {
	stylesPath := t.TempDir()
	scriptName := "NoGerundsInTitles.tengo"
	writeActionScript(t, filepath.Join(stylesPath, "RedHat", scriptName), "style")

	alert := actionTestAlert(scriptName)
	alert.Check = "NoGerundsInTitles"

	_, err := FixAlert(alert, actionTestConfig(t, stylesPath))
	if err == nil {
		t.Fatal("expected missing script error")
	}
}
