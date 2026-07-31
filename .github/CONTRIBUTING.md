# Contributing to Vale

Interested in contributing to Vale? Great&mdash;we welcome contributions of any kind including documentation improvements, bug reports, feature requests, and pull requests.

## Table of Contents

- [Introduction](#introduction)
- [Setting up a Development Environment](#setting-up-a-development-environment)
- [Testing](#testing)
- [Benchmarking](#benchmarking)
- [Code Contribution Guidelines](#code-contribution-guidelines)
- [Git Commit Message Guidelines](#git-commit-message-guidelines)
- [Terminology](#terminology)

## Introduction

Vale is a natural language linter that supports plain text, markup (Markdown, reStructuredText, AsciiDoc, HTML, DITA, XML, and more), and source code comments. Unlike many similar projects, Vale's primary focus isn't on providing a collection of rules everyone must follow&mdash;instead, Vale aims to be flexible enough to support many different styles.

Vale is written in Go. The command lives in [`cmd/vale/`](../cmd/vale/) and the implementation is split across [`internal/`](../internal/):

| Package | Responsibility |
|:--|:--|
| `check` | Vale's extension points (`existence`, `substitution`, `occurrence`, etc.), including the built-in `Vale.Terms` and `Vale.Spelling` rules. |
| `core` | Core structures used throughout the application (`File`, `Alert`, `Config`) and configuration handling. |
| `glob` | Glob matching for the file and rule filters in `.vale.ini`. |
| `lint` | The linting itself: when to apply rules and how to handle each markup format. Source-code comments are extracted by the tree-sitter parsers in [`lint/code/`](../internal/lint/code/). |
| `nlp` | POS tagging, word tokenization, and sentence segmentation. |
| `regex` | Pattern compilation and the literal prefilter that skips rules that can't match. |
| `spell` | A pure-Go spell checker built on Hunspell-compatible dictionaries. |
| `system` | Filesystem, path, and process helpers. |

Development happens on the `v3` branch, which is also the base branch for pull requests.

If you're looking to improve Vale's documentation, it lives in a separate repository and is published at [docs.vale.sh](https://docs.vale.sh/).

## Setting up a Development Environment

Prerequisites:

* [Go](https://go.dev/dl/) matching the version in [`go.mod`](../go.mod). Building requires `CGO_ENABLED=1` for the tree-sitter parsers.
* [Ruby](https://www.ruby-lang.org/en/downloads/) (v3.3) with [Bundler](https://bundler.io/), for the Cucumber suite.

The Cucumber suite shells out to external converters, so these also need to be on your `$PATH`:

* [Asciidoctor](https://asciidoctor.org/)
* [rst2html](https://docutils.sourceforge.io/docs/user/tools.html#rst2html-py), installed with [docutils](https://pypi.org/project/docutils/) or [Sphinx](https://www.sphinx-doc.org/)
* [xsltproc](http://xmlsoft.org/xslt/xsltproc.html)
* [dita](https://www.dita-ot.org/download) (v3.6+)
* [mdx2vast](https://www.npmjs.com/package/mdx2vast) (`npm install -g mdx2vast`)

Then build and test:

```bash
make setup                    # bundle install for the Cucumber suite
make build os=linux exe=vale  # writes ./bin/vale
export PATH="$PWD/bin:$PATH"  # the Cucumber steps invoke a bare `vale`
make test
```

`make build` takes `os`, `arch`, and `exe`; omitting `os` and `arch` builds for the host platform. On Windows, use `exe=vale.exe`.

## Testing

Vale is tested with both unit and integration tests, and `make test` runs both.

Unit tests are the `*_test.go` files inside the Go packages. Integration tests are written against [Cucumber](https://cucumber.io/) and live in [`testdata/`](../testdata/): `features/` holds the scenarios and step definitions, and `fixtures/` holds the configurations and documents they lint.

To run one side on its own:

```bash
# Go tests only
go test ./internal/... ./cmd/vale

# Cucumber only, or a single feature
cd testdata && cucumber --format progress
cd testdata && cucumber features/scopes.feature
```

Both suites run on Linux and Windows in [`test.yml`](workflows/test.yml). The Cucumber suite is Linux-only in CI&mdash;its `aruba`/`childprocess`/`ffi` stack can't spawn processes on modern Windows with Ruby 3.x&mdash;so Windows covers the build and the Go tests.

## Benchmarking

Benchmarks live alongside the code in `internal/core`, `internal/lint`, and `internal/check`:

```bash
make bench                    # go test -bench=. -benchmem on those packages
make profile                  # CPU, memory, and trace profiles into bin/
```

Every pull request is benchmarked against its merge base by [`bench.yml`](workflows/bench.yml), which reports a `benchstat` comparison plus peak RSS:

```text
                     │  /tmp/old.txt  │             /tmp/new.txt             │
                     │     sec/op     │    sec/op     vs base                │
LintRST-4               1.63 ± 2%       1.65 ± 2%    ~ (p=0.310 n=6)
LintMD-4                1.54 ± 1%       1.42 ± 1%  -7.79% (p=0.002 n=6)
```

Absolute timings from a shared CI runner aren't meaningful, but the ratio between two runs minutes apart on the same box is. If you're submitting a `perf` change, include the comparison in the pull request.

## Code Contribution Guidelines

To make the contribution process as seamless as possible, we ask for the following:

* Fork the project and make your changes against `v3`.
* Add or update tests for the behavior you're changing&mdash;a Cucumber scenario for user-visible behavior, a Go test for anything internal.
* When you're ready to create a pull request, be sure to:
    * Run [golangci-lint](https://golangci-lint.run/) to check your Go code. The configuration is in [`.golangci.yml`](../.golangci.yml) and CI runs v2.5.
    * Run `gofmt` (or `go fmt ./...`) on anything you've touched.
    * Squash your commits into a single commit with `git rebase -i`. It's okay to force update your pull request with `git push -f`.
    * Follow the **Git Commit Message Guidelines** below.

## Git Commit Message Guidelines

Vale follows a modified version of the [AngularJS Commit Guidelines](https://github.com/angular/angular.js/blob/master/CONTRIBUTING.md#-git-commit-guidelines). A commit message should take the following form:

```text
<type>: <subject>
<BLANK LINE>
<body>
<BLANK LINE>
<footer>
```

with `<body>` and `<footer>` being optional. `<type>` should be one of the following:

- `feat`: A new feature
- `fix`: A bug fix
- `docs`: Documentation only changes (e.g., this document, the README, or source comments)
- `style`: Changes that do not affect the meaning of the code (e.g., code formatting)
- `refactor`: A code change that neither fixes a bug nor adds a feature
- `perf`: A code change that improves performance (in this case, please include relevant benchmark(s))
- `test`: Adding missing or correcting existing tests
- `chore`: Changes to the build process or auxiliary tools

An example would be something like:

```text
refactor: make "warning" the default lint level

Also demotes `Annotations` and `PassiveVoice` to "suggestions."

Related to #30.
```

## Terminology

| Term  | Definition |
|:--|:--|
| check | A "check" is one of Vale's extension points (e.g., `existence` and `substitution`) that performs a single task such as looking for the existence of a word. The implementations live in [`internal/check/`](../internal/check/). |
| rule  | A "rule" is an actual implementation of a check, written as a YAML file. For example, `Hedging` in the `write-good` package is an `existence` rule. Browse them in the [Rule Explorer](https://vale.sh/explorer/). |
| style | A "style" is a collection of rules, distributed as a package. For example, `Joblint` is a style that consists of rules such as `LegacyTech`. Browse them in the [Package Hub](https://vale.sh/hub/). |
