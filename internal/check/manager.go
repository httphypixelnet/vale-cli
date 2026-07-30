package check

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/exp/maps"

	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/nlp"
	"github.com/errata-ai/vale/v3/internal/system"
)

// Manager controls the loading and validating of the check extension points.
type Manager struct {
	Config *core.Config

	scopes       map[string]struct{}
	rules        map[string]Rule
	styles       []string
	needsTagging bool
}

// NewManager creates a new Manager and loads the rule definitions (that is,
// extended checks) specified by configuration.
func NewManager(config *core.Config) (*Manager, error) {
	var path string

	mgr := Manager{
		Config: config,

		rules:  make(map[string]Rule),
		scopes: make(map[string]struct{}),
	}

	// TODO: Should we only load these if we're using them?
	err := mgr.loadDefaultRules()
	if err != nil {
		return &mgr, err
	}

	// Load our styles ...
	err = mgr.loadStyles(mgr.Config.Styles)
	if err != nil {
		return &mgr, err
	}

	for _, chk := range mgr.Config.Checks {
		// Load any remaining individual rules.
		if !strings.Contains(chk, ".") {
			// A rule must be associated with a style (i.e., "Style[.]Rule").
			continue
		}
		parts := strings.Split(chk, ".")
		if !mgr.hasStyle(parts[0]) {
			// If this rule isn't part of an already-loaded style, we load it
			// individually.
			fName := parts[1] + ".yml"
			for _, p := range mgr.Config.SearchPaths() {
				path = filepath.Join(p, parts[0], fName)
				if !system.FileExists(path) {
					continue
				}
				if err = mgr.addRuleFromSource(fName, path); err != nil {
					return &mgr, err
				}
			}
		}
	}

	mgr.rules, err = filter(&mgr)
	return &mgr, err
}

// AddRule adds the given rule to the manager.
func (mgr *Manager) AddRule(name string, rule Rule) error {
	if _, found := mgr.rules[name]; !found {
		mgr.rules[name] = rule
		return nil
	}
	return fmt.Errorf("the rule '%s' has already been added", name)
}

// AddRuleFromFile adds the given rule to the manager.
func (mgr *Manager) AddRuleFromFile(name, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return core.NewE100("ReadFile", err)
	}
	return mgr.addCheck(content, name, path)
}

// Rules are all of the Manager's compiled `Rule`s.
func (mgr *Manager) Rules() map[string]Rule {
	return mgr.rules
}

// HasScope returns `true` if the manager has a rule that applies to `scope`.
func (mgr *Manager) HasScope(scope string) bool {
	_, found := mgr.scopes[scope]
	return found
}

// NeedsTagging indicates if POS tagging is needed.
func (mgr *Manager) NeedsTagging() bool {
	return mgr.needsTagging
}

// AssignNLP determines what NLP tasks a file needs.
func (mgr *Manager) AssignNLP(f *core.File) nlp.Info {
	return nlp.Info{
		Scope:        f.RealExt,
		Segmentation: mgr.HasScope("sentence"),
		Splitting:    mgr.HasScope("paragraph"),
		Tagging:      mgr.NeedsTagging(),
		Endpoint:     f.NLP.Endpoint,
		Lang:         f.NLP.Lang,
	}
}

func (mgr *Manager) addStyle(path string) error {
	// Compiling a rule is the expensive half of loading one -- parsing the
	// YAML and, mostly, handing its patterns to the regular-expression engine,
	// which for a case-insensitive pattern enumerates Unicode case folds. Done
	// one rule at a time that is most of what Vale spends before it reads a
	// byte of input, and the rules are independent of each other.
	//
	// So they are compiled in parallel and registered afterwards, in the order
	// the walk found them. Registration touches shared state and stays serial;
	// keeping it ordered means the rule that wins a name clash, and the error
	// that gets reported first, do not depend on which goroutine finished
	// first.
	type source struct {
		name, path string
	}

	var sources []source
	err := system.Walk(path, func(fp string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		} else if info.IsDir() || !strings.HasSuffix(info.Name(), ".yml") {
			return nil
		}
		sources = append(sources, source{name: info.Name(), path: fp})
		return nil
	})
	if err != nil {
		return err
	}

	type result struct {
		chkName   string
		rule      Rule
		taggedPOS bool
		err       error
		skip      bool
	}

	results := make([]result, len(sources))

	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))

	for i, src := range sources {
		style := filepath.Base(filepath.Dir(src.path))
		chkName := style + "." + strings.Split(src.name, ".")[0]

		// A rule already loaded under this name is not re-read: the first
		// search path to define it wins, as before.
		if _, ok := mgr.rules[chkName]; ok {
			results[i] = result{skip: true}
			continue
		}
		results[i].chkName = chkName

		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			data, rerr := os.ReadFile(path)
			if rerr != nil {
				results[i].err = core.NewE201FromPosition(rerr.Error(), path, 1)
				return
			}
			results[i].rule, results[i].taggedPOS, results[i].err = mgr.compileCheck(
				data, results[i].chkName, path)
		}(i, src.path)
	}
	wg.Wait()

	for i := range results {
		if results[i].skip {
			continue
		}
		if results[i].err != nil {
			return results[i].err
		}
		if rerr := mgr.registerCheck(
			results[i].chkName, results[i].rule, results[i].taggedPOS); rerr != nil {
			return rerr
		}
	}

	return nil
}

func (mgr *Manager) addRuleFromSource(name, path string) error {
	if strings.HasSuffix(name, ".yml") {
		f, err := os.ReadFile(path)
		if err != nil {
			return core.NewE201FromPosition(err.Error(), path, 1)
		}

		style := filepath.Base(filepath.Dir(path))
		chkName := style + "." + strings.Split(name, ".")[0]
		if _, ok := mgr.rules[chkName]; !ok {
			if err = mgr.addCheck(f, chkName, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (mgr *Manager) addCheck(file []byte, chkName, path string) error {
	rule, taggedPOS, err := mgr.compileCheck(file, chkName, path)
	if err != nil {
		return err
	}
	return mgr.registerCheck(chkName, rule, taggedPOS)
}

// compileCheck turns a rule's source into a Rule.
//
// It reads mgr.Config but does not touch mgr's mutable state, so it is safe to
// run concurrently for different rules. Everything that writes to the Manager
// is in registerCheck.
func (mgr *Manager) compileCheck(file []byte, chkName, path string) (Rule, bool, error) {
	// Load the rule definition.
	generic, err := parse(file, path)
	if err != nil {
		return nil, false, err
	}

	// Set default values, if necessary.
	generic["name"] = chkName
	generic["path"] = path

	if level, ok := mgr.Config.RuleToLevel[chkName]; ok {
		generic["level"] = level
	} else if _, ok = generic["level"]; !ok {
		generic["level"] = "warning"
	}
	if scope, ok := generic["scope"]; scope == nil || !ok {
		generic["scope"] = []string{"text"}
	}

	rule, err := buildRule(mgr.Config, generic)
	if err != nil {
		return nil, false, err
	}

	pos, ok := generic["pos"]
	return rule, ok && pos != "", nil
}

// registerCheck records a compiled rule and what it implies for the run.
func (mgr *Manager) registerCheck(chkName string, rule Rule, taggedPOS bool) error {
	for _, s := range rule.Fields().Scope {
		base := strings.Split(s, ".")[0]
		mgr.scopes[base] = struct{}{}
	}

	if rule.Fields().Extends == "sequence" || taggedPOS {
		mgr.needsTagging = true
	}

	return mgr.AddRule(chkName, rule)
}

func (mgr *Manager) loadDefaultRules() error {
	if !mgr.needsStyle("Vale") {
		return nil
	}

	for _, style := range defaultStyles {
		if core.StringInSlice(style, mgr.styles) {
			return fmt.Errorf("'%v' collides with built-in style", style)
		}
	}

	repetition := defaultRules["Repetition"]
	if level, ok := mgr.Config.RuleToLevel["Vale.Repetition"]; ok {
		repetition["level"] = level
	}
	repetition["path"] = "internal"

	rule, err := buildRule(mgr.Config, repetition)
	if err != nil {
		return err
	}
	mgr.rules["Vale.Repetition"] = rule

	spelling := defaultRules["Spelling"]
	if level, ok := mgr.Config.RuleToLevel["Vale.Spelling"]; ok {
		spelling["level"] = level
	}
	spelling["path"] = "internal"

	rule, err = buildRule(mgr.Config, spelling)
	if err != nil {
		return err
	}
	mgr.rules["Vale.Spelling"] = rule

	// TODO: where should this go?
	mgr.loadVocabRules()

	return nil
}

func (mgr *Manager) loadStyles(styles []string) error {
	var found []string
	var need []string

	for _, baseDir := range mgr.Config.SearchPaths() {
		for _, style := range styles {
			p := filepath.Join(baseDir, style)
			if mgr.hasStyle(style) {
				// We've already loaded this style.
				continue
			} else if has := system.IsDir(p); !has {
				need = append(need, style)
				continue
			} else if err := mgr.addStyle(p); err != nil {
				return err
			}
			found = append(found, style)
		}
	}

	for _, s := range need {
		if !core.StringInSlice(s, found) {
			return core.NewE100(
				"loadStyles",
				errors.New("style '"+s+"' does not exist on StylesPath"))
		}
	}

	mgr.styles = append(mgr.styles, found...)
	return nil
}

func (mgr *Manager) loadVocabRules() {
	if len(mgr.Config.AcceptedTokens) > 0 {
		vocab := defaultRules["Terms"]
		for _, term := range mgr.Config.AcceptedTokens {
			vocab["swap"].(map[string]string)[strings.ToLower(term)] = term
		}
		if level, ok := mgr.Config.RuleToLevel["Vale.Terms"]; ok {
			vocab["level"] = level
		}
		rule, _ := buildRule(mgr.Config, vocab)
		mgr.rules["Vale.Terms"] = rule
	}

	if len(mgr.Config.RejectedTokens) > 0 {
		avoid := defaultRules["Avoid"]
		for _, term := range mgr.Config.RejectedTokens {
			avoid["tokens"] = append(avoid["tokens"].([]string), term)
		}
		if level, ok := mgr.Config.RuleToLevel["Vale.Avoid"]; ok {
			avoid["level"] = level
		}
		rule, _ := buildRule(mgr.Config, avoid)
		mgr.rules["Vale.Avoid"] = rule
	}
}

func (mgr *Manager) hasStyle(name string) bool {
	styles := append(mgr.styles, defaultStyles...) //nolint:gocritic
	return core.StringInSlice(name, styles)
}

func (mgr *Manager) needsStyle(name string) bool {
	cfg := mgr.Config

	if core.StringInSlice(name, cfg.GBaseStyles) {
		return true
	}

	for _, s := range maps.Keys(cfg.GChecks) {
		if strings.HasPrefix(s, name) {
			return true
		}
	}

	for _, s := range cfg.SBaseStyles {
		if core.StringInSlice(name, s) {
			return true
		}
	}

	for _, s := range cfg.SChecks {
		for _, chk := range maps.Keys(s) {
			if strings.HasPrefix(chk, name) {
				return true
			}
		}
	}

	return false
}
