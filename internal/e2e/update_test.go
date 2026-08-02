package e2e

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// An edit is what -update records for a case: what Vale actually printed, and
// the status it actually exited with.
type edit struct {
	want    string
	hasWant bool // false for a hand-written `contains:` excerpt
	exit    int
}

// rewrite updates the named cases in a suite file to match what Vale did.
//
// It edits the file as text rather than re-encoding it, so comments, ordering,
// and every untouched case survive verbatim.
func rewrite(path string, cases map[string]edit) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var (
		out     []string
		src     = strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
		current string
		seen    = map[string]bool{}
	)

	for i := 0; i < len(src); i++ {
		line := src[i]

		if m := caseName.FindStringSubmatch(line); m != nil {
			current = unquote(m[1])
		}

		e, ours := cases[current]
		if !ours {
			out = append(out, line)
			continue
		}
		seen[current] = true

		if exitKey.MatchString(line) {
			out = append(out, "    exit: "+strconv.Itoa(e.exit))
			continue
		}

		if !wantKey.MatchString(line) || !e.hasWant {
			out = append(out, line)
			continue
		}

		out = append(out, block("want", e.want)...)

		// Skip whatever the old block was: an inline scalar occupies just this
		// line, and a literal block runs until the indentation drops.
		if strings.HasSuffix(strings.TrimSpace(line), "|") {
			for i+1 < len(src) && (src[i+1] == "" || strings.HasPrefix(src[i+1], "      ")) {
				// A blank line only continues the block if more of it follows.
				if src[i+1] == "" && !(i+2 < len(src) && strings.HasPrefix(src[i+2], "      ")) {
					break
				}
				i++
			}
		}
	}

	for name := range cases {
		if !seen[name] {
			return fmt.Errorf("%s: no case named %q to update", path, name)
		}
	}

	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644) //nolint:gosec
}

var (
	caseName = regexp.MustCompile(`^  - name:\s*(.+?)\s*$`)
	wantKey  = regexp.MustCompile(`^    want:`)
	exitKey  = regexp.MustCompile(`^    exit:`)
)

// block renders `key` as an empty scalar or a literal block, indented to sit
// under a case.
func block(key, body string) []string {
	if body == "" {
		return []string{`    ` + key + `: ""`}
	}

	out := []string{"    " + key + ": |"}
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			out = append(out, "")
		} else {
			out = append(out, "      "+line)
		}
	}

	return out
}

func unquote(s string) string {
	if len(s) > 1 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return strings.ReplaceAll(s[1:len(s)-1], `\"`, `"`)
	}
	return s
}
