package lint

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// childProcessCount returns how many processes this one has spawned.
//
// A pool leaks by leaving its own children running, so those are the only
// processes worth counting. Matching on a name instead -- every `python` or
// `ruby` on the machine -- made the answer depend on the rest of the run:
// `go test ./...` runs packages in parallel, and the e2e suite converts
// reStructuredText and AsciiDoc through interpreters of exactly those names.
// One of them starting between the two measurements failed a test about a
// pool it had never touched.
//
// `ps` appears in its own output as a child of this process, which costs the
// same one on either side of the comparison.
func childProcessCount(t *testing.T) int {
	t.Helper()

	out, err := exec.Command("ps", "-eo", "ppid=,comm=").Output()
	if err != nil {
		t.Skip("ps unavailable")
	}

	pid := strconv.Itoa(os.Getpid())

	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if fields := strings.Fields(line); len(fields) > 1 && fields[0] == pid {
			count++
		}
	}

	return count
}
