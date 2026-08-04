package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/errata-ai/vale/v3/internal/core"
)

// enabledTestManager builds a two-rule style on disk and loads it under cfgFn's
// configuration, returning the rules the manager ended up with.
func enabledTestManager(t *testing.T, cfgFn func(*core.Config)) map[string]Rule {
	t.Helper()

	stylesPath := t.TempDir()
	styleDir := filepath.Join(stylesPath, "Demo")
	if err := os.MkdirAll(styleDir, 0o755); err != nil {
		t.Fatal(err)
	}

	rule := "extends: existence\nmessage: \"Avoid '%s'.\"\nlevel: warning\ntokens:\n  - obviously\n"
	for _, name := range []string{"Wanted.yml", "Unwanted.yml"} {
		if err := os.WriteFile(filepath.Join(styleDir, name), []byte(rule), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg, err := core.NewConfig(&core.CLIFlags{IgnoreGlobal: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg.AddStylesPath(stylesPath)
	cfg.Styles = []string{"Demo"}
	cfgFn(cfg)

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return mgr.Rules()
}

func hasRule(rules map[string]Rule, name string) bool {
	_, ok := rules[name]
	return ok
}

// A rule the configuration can never run is not compiled. Compiling is the
// expensive half of loading, and `= NO` was applied long after it.
func TestDisabledRuleIsNotCompiled(t *testing.T) {
	rules := enabledTestManager(t, func(cfg *core.Config) {
		cfg.SBaseStyles["*.md"] = []string{"Demo"}
		cfg.SChecks["*.md"] = map[string]bool{"Demo.Unwanted": false}
	})

	if !hasRule(rules, "Demo.Wanted") {
		t.Error("an enabled rule must still load")
	}
	if hasRule(rules, "Demo.Unwanted") {
		t.Error("a rule disabled in every section must not be compiled")
	}
}

// A style nothing is based on, and no setting names, is not compiled at all.
func TestUnreferencedStyleIsNotCompiled(t *testing.T) {
	rules := enabledTestManager(t, func(cfg *core.Config) {
		cfg.SBaseStyles["*.md"] = []string{"Vale"}
	})

	if hasRule(rules, "Demo.Wanted") || hasRule(rules, "Demo.Unwanted") {
		t.Error("a style no section is based on must not be compiled")
	}
}

// The skip has to err toward compiling: a rule switched off globally but back
// on for one section still runs on that section's files.
func TestSectionCanReEnableADisabledRule(t *testing.T) {
	rules := enabledTestManager(t, func(cfg *core.Config) {
		cfg.GChecks["Demo.Unwanted"] = false
		cfg.SBaseStyles["*.md"] = []string{"Demo"}
		cfg.SChecks["*.txt"] = map[string]bool{"Demo.Unwanted": true}
	})

	if !hasRule(rules, "Demo.Unwanted") {
		t.Error("a rule re-enabled by any section must be compiled")
	}
}

// A whole style off, one rule of it back on: the named rule wins over its
// style, the same precedence lint.lookup applies.
func TestRuleSettingWinsOverStyleSetting(t *testing.T) {
	rules := enabledTestManager(t, func(cfg *core.Config) {
		cfg.SBaseStyles["*.md"] = []string{"Demo"}
		cfg.SChecks["*.md"] = map[string]bool{"Demo": false, "Demo.Wanted": true}
	})

	if !hasRule(rules, "Demo.Wanted") {
		t.Error("a rule enabled beside a disabled style must be compiled")
	}
	if hasRule(rules, "Demo.Unwanted") {
		t.Error("the rest of a disabled style must not be compiled")
	}
}

// A rule named on its own, with no BasedOnStyles at all, still loads.
func TestRuleEnabledWithoutBaseStyles(t *testing.T) {
	rules := enabledTestManager(t, func(cfg *core.Config) {
		cfg.SChecks["*.md"] = map[string]bool{"Demo.Wanted": true}
	})

	if !hasRule(rules, "Demo.Wanted") {
		t.Error("a rule enabled by name must be compiled without BasedOnStyles")
	}
	if hasRule(rules, "Demo.Unwanted") {
		t.Error("naming one rule must not pull in the rest of its style")
	}
}
