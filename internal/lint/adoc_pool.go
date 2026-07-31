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

// A pool of long-lived Asciidoctor processes.
//
// Converting a document costs a few milliseconds; starting Ruby and loading
// Asciidoctor costs about fifty. Vale was paying the latter once per file, so
// on a corpus of a few hundred AsciiDoc pages nearly all of the time went to
// starting the same program over and over.
//
// These processes stay up for the run and convert documents as they arrive.
// There are several because Vale lints files concurrently and a single process
// would serialise them; each one handles a request at a time, and the pool is
// sized to the number of files Vale has in flight.
//
// The framing matches mdx2vast's, and for the same reason: AsciiDoc can contain
// any byte sequence a delimiter might use, so requests carry a length.
//
//	request   <byteLength> LF <bytes>
//	response  "ok " <byteLength> LF <bytes>
//	          "err " <byteLength> LF <message>
//
// A document that fails to convert answers `err` and the process stays up. One
// unparseable file must not cost the rest of the run its warm processes.
const adocServer = `Encoding.default_external = Encoding::UTF_8
Encoding.default_internal = Encoding::UTF_8
$stdin.binmode
$stdout.binmode

require "asciidoctor"

attrs = {}
ARGV.each { |a| k, _, v = a.partition("="); attrs[k] = v }

while (header = $stdin.gets)
  n = header.to_i
  break if n < 0
  doc = n.zero? ? "" : $stdin.read(n)
  break if doc.nil?

  begin
    out = Asciidoctor.convert(doc.force_encoding("UTF-8"),
      standalone: false, safe: :secure, attributes: attrs).to_s
    $stdout.write("ok #{out.bytesize}\n", out)
  rescue => e
    msg = e.message.to_s
    $stdout.write("err #{msg.bytesize}\n", msg)
  end
  $stdout.flush
end`

// adocProc is one Asciidoctor process and the pipes to talk to it.
type adocProc struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func startAdocProc(argv, attrs []string) (*adocProc, error) {
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

	return &adocProc{cmd: cmd, in: stdin, out: bufio.NewReaderSize(stdout, 64<<10)}, nil
}

// convert sends one document and reads its reply.
func (p *adocProc) convert(text string) (string, error) {
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

func (p *adocProc) close() {
	_ = p.in.Close()
	_ = p.cmd.Wait()
}

// adocPool hands out warm processes.
type adocPool struct {
	free chan *adocProc
	mu   sync.Mutex
	all  []*adocProc
}

func newAdocPool(argv, attrs []string, size int) (*adocPool, error) {
	pool := &adocPool{free: make(chan *adocProc, size)}

	for i := 0; i < size; i++ {
		proc, err := startAdocProc(argv, attrs)
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
func (pool *adocPool) convert(text string, argv, attrs []string) (string, error) {
	proc := <-pool.free

	out, err := proc.convert(text)
	if err != nil && !strings.HasPrefix(err.Error(), "asciidoctor:") {
		proc.close()

		replacement, startErr := startAdocProc(argv, attrs)
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

func (pool *adocPool) stop() {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	for _, proc := range pool.all {
		proc.close()
	}
	pool.all = nil
}
