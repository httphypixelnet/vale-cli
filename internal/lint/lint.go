package lint

import (
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/remeh/sizedwaitgroup"

	"github.com/errata-ai/vale/v3/internal/check"
	"github.com/errata-ai/vale/v3/internal/core"
	"github.com/errata-ai/vale/v3/internal/glob"
	"github.com/errata-ai/vale/v3/internal/nlp"
	"github.com/errata-ai/vale/v3/internal/system"
)

// A Linter lints a File.
type Linter struct {
	Manager   *check.Manager
	glob      *glob.Glob
	client    *http.Client
	HasDir    bool
	nonGlobal bool
	metaScope string

	// adoc holds the Asciidoctor processes this run is using, and adocOnce
	// starts them the first time an AsciiDoc file is seen.
	//
	// These belong to the Linter rather than to the package: a long-lived
	// caller -- the language server, or anything embedding Vale -- lints many
	// times in one process, and a pool with no owner is a pool nothing ever
	// stops.
	adoc     *adocPool
	adocOnce sync.Once

	// inScope lists the rules whose scope matches a given block scope, keyed by
	// the block's scope and parent.
	//
	// Whether a rule's scope matches depends on nothing else, and a document
	// has a handful of distinct block scopes against several hundred rules --
	// so the answer was being recomputed for every rule on every block, which
	// was the largest part of deciding what to run.
	inScope *sync.Map
}

// scopedRule is a rule together with the name it was registered under.
type scopedRule struct {
	name string
	rule check.Rule
}

type lintResult struct {
	file *core.File
	err  error
}

// NewLinter initializes a Linter.
func NewLinter(cfg *core.Config) (*Linter, error) {
	mgr, err := check.NewManager(cfg)

	globalStyles := len(cfg.GBaseStyles)
	globalChecks := len(cfg.GChecks)

	return &Linter{
		Manager: mgr,
		inScope: &sync.Map{},

		client:    http.DefaultClient,
		nonGlobal: globalStyles+globalChecks == 0}, err
}

// Transform applies the configured transformations to text and returns the
// result.
//
// This is used by the `vale` command to apply transformations to text before
// linting it.
//
// Transformations include block and token ignores, as well as some built-in
// replacements.
func (l *Linter) Transform(f *core.File) (string, error) {
	exts := extensionConfig{
		Normed: f.NormedExt,
		Real:   f.RealExt,
	}

	return applyPatterns(l.Manager.Config, exts, f.Content)
}

// LintString src according to its format.
func (l *Linter) LintString(src string) ([]*core.File, error) {
	linted := l.lintFile(src)
	return []*core.File{linted.file}, linted.err
}

// SetMetaScope sets an optional meta scope.
//
// A meta scope is a string that is appended to the end of each check's scope
// providing extra context for the check.
func (l *Linter) SetMetaScope(scope string) {
	if scope != "" {
		l.metaScope = "." + scope
	} else {
		l.metaScope = ""
	}
}

// Lint src according to its format.
func (l *Linter) Lint(input []string, pat string) ([]*core.File, error) {
	var linted []*core.File

	done := make(chan core.File)
	defer close(done)

	gp, err := glob.NewGlob(pat)
	if err != nil {
		return linted, err
	}

	l.glob = &gp

	// Whatever external processes this run starts, it also stops. Lint may be
	// called again on the same Linter, so the next run starts its own.
	defer l.stopExternal()

	for _, src := range input {
		filesChan, errChan := l.lintFiles(done, src)

		for result := range filesChan {
			if result.err != nil {
				return linted, result.err
			} else if l.Manager.Config.Flags.Normalize {
				result.file.Path = filepath.ToSlash(result.file.Path)
			}
			linted = append(linted, result.file)
		}

		if err = <-errChan; err != nil {
			return linted, err
		}
	}

	return linted, nil
}

// lintFiles walks the `root` directory, creating a new goroutine to lint any
// file that matches the given glob pattern.
func (l *Linter) lintFiles(done <-chan core.File, root string) (<-chan lintResult, <-chan error) {
	filesChan := make(chan lintResult)
	errChan := make(chan error, 1)

	go func() {
		wg := sizedwaitgroup.New(5)

		err := system.Walk(root, func(fp string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if info.IsDir() && core.ShouldIgnoreDirectory(fp) {
				return filepath.SkipDir
			} else if info.IsDir() || l.skip(fp) {
				return nil
			}

			wg.Add()
			go func(fp string) {
				select {
				case filesChan <- l.lintFile(fp):
				case <-done:
				}
				wg.Done()
			}(fp)

			// Abort the walk if done is closed.
			select {
			case <-done:
				return errors.New("walk canceled")
			default:
				return nil
			}
		})

		// Walk has returned, so all calls to wg.Add are done.  Start a
		// goroutine to close c once all the sends are done.
		go func() {
			wg.Wait()
			close(filesChan)
		}()
		errChan <- err
	}()

	return filesChan, errChan
}

// lintFile creates a new `File` from the path `src` and selects a linter based
// on its format.
func (l *Linter) lintFile(src string) lintResult {
	var err error

	file, err := core.NewFile(src, l.Manager.Config)
	if err != nil {
		return lintResult{err: err}
	} else if len(file.Checks) == 0 && len(file.BaseStyles) == 0 {
		if len(l.Manager.Config.GBaseStyles) == 0 && len(l.Manager.Config.GChecks) == 0 {
			// There's nothing to do; bail early.
			return lintResult{file: file}
		}
	}

	// Determine what NLP tasks this particular file needs; the goal is to do
	// the least amount of work possible.
	file.NLP = l.Manager.AssignNLP(file)
	simple := l.Manager.Config.Flags.Simple

	// NOTE: This is a sanity check to ensure that we don't run any checks that
	// we actually have a View to apply.
	hasViews := len(l.Manager.Config.Views) > 0

	if file.Format == "markup" && !simple { //nolint:gocritic
		switch file.NormedExt {
		case ".adoc":
			err = l.lintADoc(file)
		case ".md":
			err = l.lintMarkdown(file)
		case ".mdx":
			err = l.lintMDX(file)
		case ".rst":
			err = l.lintRST(file)
		case ".xml", ".xsd":
			err = l.lintXML(file)
		case ".dita":
			err = l.lintDITA(file)
		case ".html":
			err = l.lintHTML(file)
		case ".org":
			err = l.lintOrg(file)
		}
	} else if file.Format == "data" && !simple && hasViews {
		err = l.lintData(file)
	} else if file.Format == "code" && !simple {
		err = l.lintCode(file)
	} else if file.Format == "fragment" && !simple {
		err = l.lintFragments(file)
	} else if file.NormedExt == ".txt" && !simple {
		err = l.lintTxt(file)
	} else {
		err = l.lintLines(file)
	}

	if err == nil {
		// Run all rules with `scope: raw`
		//
		// NOTE: We need to use `f.Lines` (instead of `f.Content`) to ensure
		// that we don't include any markup preprocessing.
		//
		// See #248, #306.
		raw := nlp.NewBlock("", strings.Join(file.Lines, ""), "raw"+file.RealExt)
		err = l.lintBlock(file, raw, len(file.Lines), 0, true)
	}

	return lintResult{file, err}
}

func (l *Linter) lintProse(f *core.File, blk nlp.Block, lines int) error {
	blks, err := f.NLP.Compute(&blk)
	if err != nil {
		return core.NewE100("NLP.Compute", err)
	}

	// FIXME: This is required for paragraphs that lack a newline delimiter:
	//
	// p1
	// p2
	//
	// See fixtures/i18n for an example.
	needsLookup := strings.Count(blk.Text, "\n") > 0 || f.Lookup

	// Segmenting hands back blocks that differ in scope but not always in
	// text, so a rule matching both runs twice over the same string. The second
	// run reports nothing the first did not, and it was once worth tracking
	// which rules had already seen a text to skip it.
	//
	// It no longer is: the prefilter now turns a repeat away cheaply, leaving
	// the bookkeeping to cost more than the work it saved -- 38% of the peak
	// memory on a plain-text run, for no time back.
	for _, b := range blks {
		err = l.lintBlock(f, b, lines, 0, needsLookup)
		if err != nil {
			return err
		}
	}

	return nil
}

func (l *Linter) lintTxt(f *core.File) error {
	block := nlp.NewBlock("", f.Content, "text"+l.metaScope+f.RealExt)
	return l.lintProse(f, block, len(f.Lines))
}

func (l *Linter) lintLines(f *core.File) error {
	block := nlp.NewBlock("", f.Content, "text"+l.metaScope+f.RealExt)
	return l.lintBlock(f, block, len(f.Lines), 0, true)
}

// lintBlock runs every applicable rule over blk.
func (l *Linter) lintBlock(f *core.File, blk nlp.Block, lines, pad int, lookup bool) error {
	f.ChkToCtx = make(map[string]string)
	for _, r := range l.inScopeFor(blk) {
		name, chk := r.name, r.rule
		if !l.shouldRun(name, f, chk) {
			continue
		}

		info := chk.Fields()

		alerts, err := chk.Run(blk, f, l.Manager.Config)
		if err != nil {
			return err
		}
		for i := range alerts {
			if f.QueryComments(name + "[" + alerts[i].Match + "]") {
				continue
			}
			core.FormatAlert(&alerts[i], info.Limit, info.Level, name)
			f.AddAlert(alerts[i], blk, lines, pad, lookup)
		}
	}

	return nil
}

// lookup reads a setting given for a rule, falling back to one given for the
// style it belongs to.
func lookup(settings map[string]bool, rule, style string) (bool, bool) {
	if val, ok := settings[rule]; ok {
		return val, true
	}
	val, ok := settings[style]
	return val, ok
}

// inScopeFor returns the rules that could run on blk, by scope alone.
//
// Built once per distinct block scope and reused. Everything else shouldRun
// weighs -- in-text comments, the file's own settings, the minimum level --
// varies per file and is still decided there.
func (l *Linter) inScopeFor(blk nlp.Block) []scopedRule {
	key := blk.Scope + "\x00" + blk.Parent
	if l.inScope != nil {
		if hit, ok := l.inScope.Load(key); ok {
			return hit.([]scopedRule) //nolint:errcheck // only []scopedRule is stored
		}
	}

	rules := l.Manager.Rules()
	found := make([]scopedRule, 0, len(rules))
	for name, chk := range rules {
		if check.NewScope(chk.Fields().Scope).Matches(blk) {
			found = append(found, scopedRule{name: name, rule: chk})
		}
	}

	// A stable order, so that two blocks of the same scope are linted in the
	// same sequence rather than in whatever order the map produced.
	sort.Slice(found, func(i, j int) bool { return found[i].name < found[j].name })

	if l.inScope != nil {
		l.inScope.Store(key, found)
	}

	return found
}

func (l *Linter) shouldRun(name string, f *core.File, chk check.Rule) bool {
	minLevel := l.Manager.Config.MinAlertLevel
	run := false

	details := chk.Fields()
	if strings.Count(name, ".") > 1 {
		// NOTE: This fixes the loading issue with consistency checks.
		//
		// See #129.
		list := strings.Split(name, ".")
		name = strings.Join([]string{list[0], list[1]}, ".")
	}

	if f.QueryComments(name) {
		// It has been disabled via an in-text comment.
		return false
	} else if core.LevelToInt[details.Level] < minLevel {
		return false
	}

	style := core.StyleName(name)

	// Has the check been disabled for this extension?
	//
	// The rule's own setting is looked for first and the style's only after,
	// so that `proselint = NO` can turn a style off while
	// `proselint.Typography = YES` keeps one of its rules.
	if val, ok := lookup(f.Checks, name, style); ok && !run {
		if !val {
			return false
		}
		run = true
	}

	// Has the check been disabled for all extensions?
	if val, ok := lookup(l.Manager.Config.GChecks, name, style); ok && !run {
		if !val {
			return false
		}
		run = true
	}

	if !run && !core.StringInSlice(style, f.BaseStyles) {
		return false
	}

	return true
}

func (l *Linter) match(s string) bool {
	if l.glob == nil {
		return true
	}
	return l.glob.Match(s)
}

func (l *Linter) skip(old string) bool {
	ref := filepath.ToSlash(system.ReplaceFileExt(old, l.Manager.Config.Formats))

	if !l.match(old) && !l.match(ref) {
		return true
	} else if l.nonGlobal {
		for _, pat := range l.Manager.Config.SecToPat {
			if pat.Match(old) || pat.Match(ref) {
				return false
			}
		}
		return true
	}

	return false
}

// stopExternal shuts down the helper processes a run started.
func (l *Linter) stopExternal() {
	if l.adoc != nil {
		l.adoc.stop()
		l.adoc = nil
		l.adocOnce = sync.Once{}
	}
}
