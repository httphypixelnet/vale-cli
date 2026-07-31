package lint

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// A pool of long-lived helper processes.
//
// Vale hands AsciiDoc to Asciidoctor and MDX to mdx2vast. Converting a document
// costs a few milliseconds either way; starting the interpreter and loading the
// library costs fifty to a hundred and sixty. Vale was paying the latter once
// per file, so on a corpus of a few hundred pages nearly all of the time went
// to starting the same program over and over.
//
// These processes stay up for the run and convert documents as they arrive.
// There are several because Vale lints files concurrently and a single process
// would serialise them; each one handles a request at a time, and the pool is
// sized to the number of files Vale has in flight.
//
// Both helpers speak the same framing, and for the same reason: a document can
// contain any byte sequence a delimiter might use, so requests carry a length.
//
//	request   <byteLength> LF <bytes>
//	response  "ok " <byteLength> LF <bytes>
//	          "err " <byteLength> LF <message>
//
// A document that fails to convert answers `err` and the process stays up. One
// unparseable file must not cost the rest of the run its warm processes.

// extProc is one Asciidoctor process and the pipes to talk to it.
type extProc struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func startExtProc(argv, attrs []string) (*extProc, error) {
	args := append(append([]string{}, argv[1:]...), attrs...)

	cmd := exec.Command(argv[0], args...) //nolint:gosec // argv is ours
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err = cmd.Start(); err != nil {
		return nil, err
	}

	return &extProc{cmd: cmd, in: stdin, out: bufio.NewReaderSize(stdout, 64<<10)}, nil
}

// convert sends one document and reads its reply.
func (p *extProc) convert(text string) (string, error) {
	if _, err := fmt.Fprintf(p.in, "%d\n", len(text)); err != nil {
		return "", err
	}
	if _, err := io.WriteString(p.in, text); err != nil {
		return "", err
	}

	header, err := p.out.ReadString('\n')
	if err != nil {
		return "", err
	}

	status, size, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found {
		return "", fmt.Errorf("bad reply: %q", header)
	}

	n, err := strconv.Atoi(size)
	if err != nil {
		return "", fmt.Errorf("bad reply length: %q", header)
	}

	body := make([]byte, n)
	if _, err = io.ReadFull(p.out, body); err != nil {
		return "", err
	}

	if status == "err" {
		return "", fmt.Errorf("%s", body)
	}
	return string(body), nil
}

func (p *extProc) close() {
	_ = p.in.Close()
	_ = p.cmd.Wait()
}

// procPool hands out warm processes.
type procPool struct {
	free chan *extProc
	mu   sync.Mutex
	all  []*extProc
}

func newProcPool(argv, attrs []string, size int) (*procPool, error) {
	pool := &procPool{free: make(chan *extProc, size)}

	for i := 0; i < size; i++ {
		proc, err := startExtProc(argv, attrs)
		if err != nil {
			pool.stop()
			return nil, err
		}
		pool.all = append(pool.all, proc)
		pool.free <- proc
	}

	return pool, nil
}

// convert borrows a process, uses it, and puts it back.
//
// A process that errors at the protocol level is replaced rather than reused:
// its pipes may hold a partial reply, and every later document on it would be
// read out of step.
func (pool *procPool) convert(text string, argv, attrs []string) (string, error) {
	proc := <-pool.free

	out, err := proc.convert(text)
	if err != nil && !strings.HasPrefix(err.Error(), "asciidoctor:") {
		proc.close()

		replacement, startErr := startExtProc(argv, attrs)
		if startErr != nil {
			pool.free <- proc // nothing better to offer
			return "", err
		}

		pool.mu.Lock()
		pool.all = append(pool.all, replacement)
		pool.mu.Unlock()

		pool.free <- replacement
		return "", err
	}

	pool.free <- proc
	return out, err
}

func (pool *procPool) stop() {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	for _, proc := range pool.all {
		proc.close()
	}
	pool.all = nil
}
