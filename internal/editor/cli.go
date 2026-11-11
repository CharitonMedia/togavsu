package editor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CharitonMedia/togavsu/internal/domain"
	"github.com/CharitonMedia/togavsu/internal/settings"
)

// Limits are the Document editor tab's run limits (PRD 8.20 and 7).
type Limits struct {
	Concurrent  int
	Timeout     time.Duration
	MemoryBytes int64
}

// DefaultLimits are the Document editor tab's defaults from the settings registry (PRD:
// 2 at once, 60 seconds and 1 GB each).
func DefaultLimits() Limits {
	var lim Limits
	for _, d := range settings.Definitions() {
		switch d.Key {
		case "editor.max_runs":
			n, _ := strconv.Atoi(d.Default)
			lim.Concurrent = n
		case "editor.run_timeout":
			lim.Timeout, _ = time.ParseDuration(d.Default)
		case "editor.run_memory_bytes":
			lim.MemoryBytes, _ = strconv.ParseInt(d.Default, 10, 64)
		}
	}
	return lim
}

// zero is the integer origin for counts and comparisons.
const zero = int64(0)

// ErrBusy is returned when the configured number of runs is already going.
var ErrBusy = &domain.APIError{Code: domain.CodeRateLimited, Message: "the editor is already running as many jobs as the Document editor tab allows; try again shortly"}

// CLI runs wordcraft-cli, the headless LawCraft build, inside a sandbox: a fresh
// temporary folder that is deleted afterward and holds the only files the process is
// given, an environment with nothing from the core's own, every proxy pointed at a
// closed local port, a memory limit, a time limit that kills the whole process group,
// and, where the kernel allows it, a network namespace with no interfaces. Commands that
// reach the file system or the network are refused before the process starts
// (commands.go); the sandbox is the second line.
type CLI struct {
	// Path is the wordcraft-cli binary built from the pinned commit.
	Path string
	// TempDir is where run folders are made ("" is the system default).
	TempDir string
	// Limits reads the Document editor tab; nil uses DefaultLimits.
	Limits func(ctx context.Context) (Limits, error)
	// RequireNetworkNamespace refuses to run when no network namespace can be made.
	RequireNetworkNamespace bool

	mu      sync.Mutex
	running int

	probe    sync.Once
	isolated bool

	cmdMu sync.Mutex
	cmds  []CommandInfo
}

// RunInput is one job: the document (nil starts from a blank one), what to do with it
// and the format to write.
type RunInput struct {
	Document []byte
	Ext      string
	Commands []Command
	// Profiles are court profiles, by LawCraft id, put in the run's own profile folder so
	// law.profiles.apply finds the firm's profiles.
	Profiles map[string][]byte
	Out      string
	Author   string
}

// NetworkIsolated reports whether runs get a network namespace of their own.
func (c *CLI) NetworkIsolated() bool {
	c.probe.Do(func() { c.isolated = probeNetNS() })
	return c.isolated
}

func (c *CLI) limits(ctx context.Context) (Limits, error) {
	if c.Limits == nil {
		return DefaultLimits(), nil
	}
	return c.Limits(ctx)
}

// slot takes one of the concurrent run slots or reports ErrBusy.
func (c *CLI) slot(max int) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running >= max {
		// ponytail: a run over the limit is refused (429, Retry-After) rather than
		// queued. Ceiling: callers retry. Upgrade: a fair wait queue bounded by the
		// request deadline. Trigger: agents report repeated rate_limited editor runs.
		return nil, ErrBusy
	}
	c.running++
	return func() {
		c.mu.Lock()
		c.running--
		c.mu.Unlock()
	}, nil
}

// Convert writes doc (in format ext) as format out.
func (c *CLI) Convert(ctx context.Context, doc []byte, ext, out string) ([]byte, error) {
	return c.job(ctx, doc, ext, out, nil, func(in, outPath string) ([]string, []byte, error) {
		return []string{"convert", in, outPath}, nil, nil
	})
}

// Run runs the commands on the document and returns the result written as in.Out.
func (c *CLI) Run(ctx context.Context, in RunInput) ([]byte, error) {
	return c.job(ctx, in.Document, in.Ext, in.Out, in.Profiles, func(inPath, outPath string) ([]string, []byte, error) {
		type step struct {
			Command string         `json:"command"`
			Params  map[string]any `json:"params,omitempty"`
		}
		script := struct {
			File     string   `json:"file,omitempty"`
			Commands []step   `json:"commands"`
			Save     []string `json:"save"`
		}{Save: []string{outPath}, Commands: []step{}}
		if inPath != "" {
			script.File = inPath
		}
		for _, cmd := range in.Commands {
			script.Commands = append(script.Commands, step{Command: cmd.ID, Params: cmd.Parameters})
		}
		body, err := json.Marshal(script)
		if err != nil {
			return nil, nil, err
		}
		args := []string{"run", "--script", "-"}
		if in.Author != "" {
			args = append(args, "--author", in.Author)
		}
		return args, body, nil
	})
}

func (c *CLI) job(ctx context.Context, doc []byte, ext, out string, profiles map[string][]byte,
	build func(in, out string) ([]string, []byte, error)) ([]byte, error) {
	lim, err := c.limits(ctx)
	if err != nil {
		return nil, err
	}
	release, err := c.slot(lim.Concurrent)
	if err != nil {
		return nil, err
	}
	defer release()
	dir, err := os.MkdirTemp(c.TempDir, "editor-run-")
	if err != nil {
		return nil, fmt.Errorf("editor: run folder: %w", err)
	}
	defer os.RemoveAll(dir)
	inPath := ""
	if doc != nil {
		inPath = filepath.Join(dir, "document."+normExt(ext))
		if err := os.WriteFile(inPath, doc, fs.ModePerm&0o600); err != nil {
			return nil, err
		}
	}
	for id, body := range profiles {
		if !safeProfileID(id) {
			return nil, domain.Invalid("profile", "court profile id must be letters, digits, hyphens or underscores")
		}
		pdir := filepath.Join(dir, "profile", "law", "profiles")
		if err := os.MkdirAll(pdir, fs.ModePerm&0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(pdir, id+".json"), body, fs.ModePerm&0o600); err != nil {
			return nil, err
		}
	}
	outPath := filepath.Join(dir, "result."+normExt(out))
	args, stdin, err := build(inPath, outPath)
	if err != nil {
		return nil, err
	}
	if _, err := c.exec(ctx, lim, dir, args, stdin); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		return nil, domain.Invalid("commands", "the editor wrote no document")
	}
	return b, nil
}

// CommandList is `wordcraft-cli commands --json` at the pinned commit, with LawCraft on.
// A good listing is kept for the life of the process; a failed one is tried again.
func (c *CLI) CommandList(ctx context.Context) ([]CommandInfo, error) {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()
	if c.cmds != nil {
		return c.cmds, nil
	}
	dir, err := os.MkdirTemp(c.TempDir, "editor-commands-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out, err := c.exec(ctx, DefaultLimits(), dir, []string{"commands", "--json"}, nil)
	if err != nil {
		return nil, err
	}
	var all []CommandInfo
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, fmt.Errorf("editor: wordcraft-cli commands --json: %w", err)
	}
	if len(all) == 0 {
		return nil, errors.New("editor: wordcraft-cli listed no commands")
	}
	c.cmds = all
	return all, nil
}

// ponytail: network isolation is a namespace only where the host lets the core make one;
// Docker's default seccomp profile and Ubuntu's AppArmor userns rule do not, and then a
// run is kept off the network by the command allowlist and the closed proxy alone.
// Ceiling: a LawCraft command that opens a socket without honouring the proxy variables
// and is not on the deny list. Upgrade: run wordcraft-cli in a no-network sidecar, or give
// the core a seccomp profile that allows unshare(CLONE_NEWUSER|CLONE_NEWNET), and set
// RequireNetworkNamespace. Trigger: release hardening, or any allowed command found to
// reach the network at a moved pin.
//
// closedProxy is a local port nothing listens on: an HTTP client that honours the proxy
// variables fails at once instead of reaching the network.
const closedProxy = "http://127.0.0.1:9"

func (c *CLI) exec(ctx context.Context, lim Limits, dir string, args []string, stdin []byte) ([]byte, error) {
	if c.Path == "" {
		return nil, errors.New("editor: wordcraft-cli is not configured")
	}
	if c.RequireNetworkNamespace && !c.NetworkIsolated() {
		return nil, errors.New("editor: runs require a network namespace and this host cannot make one")
	}
	timeout := lim.Timeout
	if timeout <= time.Duration(zero) {
		timeout = DefaultLimits().Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name, argv := c.Path, args
	if lim.MemoryBytes > zero {
		// ponytail: the limit is the address space (RLIMIT_AS), not resident memory.
		// Ceiling: virtual reservations count, so a run can stop below 1 GB of real use.
		// Upgrade: a cgroup v2 memory.max per run. Trigger: a legitimate document run
		// fails at the limit while using well under it.
		// ulimit -v is the address-space limit in KiB, set in the child before it becomes
		// wordcraft-cli; the shell is gone by then (exec).
		kib := fmt.Sprint((lim.MemoryBytes + 1<<10 - 1) >> 10)
		name = "/bin/sh"
		argv = append([]string{"-c", `ulimit -v "$1" || exit 125; shift; exec "$@"`, "sh", kib, c.Path}, args...)
	}
	cmd := exec.CommandContext(ctx, name, argv...)
	cmd.Dir = dir
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + dir,
		"TMPDIR=" + dir,
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"WORDCRAFT_PROFILE_DIR=" + filepath.Join(dir, "profile"),
		"WORDCRAFT_LAW=1",
		"WORDCRAFT_REGION=en-US",
		"LANG=C.UTF-8",
		"http_proxy=" + closedProxy, "https_proxy=" + closedProxy, "HTTP_PROXY=" + closedProxy,
		"HTTPS_PROXY=" + closedProxy, "ALL_PROXY=" + closedProxy, "all_proxy=" + closedProxy, "NO_PROXY=", "no_proxy=",
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr capped
	stdout.max, stderr.max = 64<<20, 64<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	sandbox(cmd, c.NetworkIsolated())
	// A process that escaped the group cannot hold the pipes open past another full limit.
	cmd.WaitDelay = timeout
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, domain.Invalid("commands", fmt.Sprintf("the editor run took longer than %s and was stopped", timeout))
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		msg := strings.TrimSpace(strings.ReplaceAll(stderr.buf.String(), dir+string(filepath.Separator), ""))
		msg = strings.ReplaceAll(msg, dir, ".")
		// The last line says what went wrong; earlier ones are LawCraft's progress.
		if _, last, ok := cutLast(msg, "\n"); ok {
			msg = last
		}
		if msg == "" {
			msg = err.Error()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, domain.Invalid("commands", "the editor could not finish: "+strings.TrimPrefix(msg, "wordcraft-cli: "))
		}
		return nil, fmt.Errorf("editor: wordcraft-cli: %w", err)
	}
	if stdout.over {
		return nil, errors.New("editor: wordcraft-cli wrote too much output")
	}
	return stdout.buf.Bytes(), nil
}

// capped keeps the first max bytes written.
type capped struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room < len(p) {
		c.over = true
		if room > int(zero) {
			c.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

var _ io.Writer = (*capped)(nil)

// profileID is a LawCraft profile id that is safe as a file name in a run's folder.
var profileID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func safeProfileID(id string) bool { return profileID.MatchString(id) }

func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i != -1 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}
