// Command o-qbt is the family tool for qBittorrent (Web API v2) on the NAS:
// the server's version and speeds as a credentials test, torrents and
// categories as rows, transfer limits, and the free-space guard that pauses
// every torrent before a download fills the volume. It is an oden app (so
// o qbt ARGS runs it) with an ochazuke runtime behind ochazuke/cli
// (shell.go): every noun's verbs are operations, lists declare their rows,
// app state answers every list as JSON Lines, and catalog describes it all
// for a host (nabe).
//
//	o-qbt config init|show|set KEY [VALUE] [--stdin]
//	o-qbt app info|prefs|state
//	o-qbt torrent list|active|seeding|downloading|paused|errored|stalled [--filter F] [--category C] [--tag T] [--sort FIELD] [--reverse] [--limit N]
//	o-qbt torrent export --out FILE | summary | overview  (o-qbt overview)
//	o-qbt torrent show|pause|resume|recheck|delete [--target HASH|--all]
//
// Every list-like verb takes --format table|json|jsonl|tsv|csv|md|toon,
// --columns a,b,c and --out FILE (format.go).
//
//	o-qbt transfer info|limit [--dl N] [--up N]|speed-limits-toggle
//	o-qbt guard check|status|rebase|resume|install|uninstall|log [N]
//	o-qbt category list
//	o-qbt catalog
//
// A verb a person reads (app info, guard status, config show, ...) prints a
// short table; with --json '{...}' (how a host calls) every verb prints JSON.
// qBittorrent bans an IP after repeated failed logins: a process logs in at
// most once and never retries.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
	"golang.org/x/term"
)

// Tool is one invocation's world: streams, environment, home, and the
// seams a test replaces (the clock, df over ssh, external commands).
type Tool struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Env            []string
	Home           string
	UID            int
	Prog           string // "o qbt" under o, else "o-qbt"; messages only
	TTY            bool   // stdout is a terminal: list verbs default to table

	now   func() time.Time
	df    func(context.Context, Config) (int64, error)
	run   func(ctx context.Context, name string, args ...string) (string, error)
	cl    *Client // this process's session: one login at most
	text  *string // a person's rendering of the last answer (Texter), when human
	human bool
}

// Texter is an answer with a rendering for a person.
type Texter interface{ Text() string }

func main() {
	home, err := os.UserHomeDir()
	if err == nil {
		home, err = filepath.Abs(home)
	}
	prog := progName(os.Getenv)
	if err != nil {
		os.Exit(oden.Report(os.Stderr, prog, err))
	}
	t := &Tool{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Env: os.Environ(), Home: home, UID: os.Getuid(), Prog: prog, TTY: term.IsTerminal(int(os.Stdout.Fd()))}
	os.Exit(oden.Report(os.Stderr, t.Prog, t.Main(context.Background(), os.Args[1:])))
}

// progName is how messages name this command: the word under the parent
// oden app that ran it, else the bin name.
func progName(getenv func(string) string) string {
	if parent := getenv("ODEN_PARENT"); parent != "" {
		return parent + " qbt"
	}
	return "o-qbt"
}

func (t *Tool) getenv(name string) string {
	for i := len(t.Env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(t.Env[i], "="); ok && k == name {
			return v
		}
	}
	return ""
}

// Main runs one command: a noun of the runtime (oden resolves a unique
// prefix, the shell a verb's), catalog, an oden builtin, a shortcut
// (torrents is torrent list, overview torrent overview), the shell's usage
// with no words, else oden's not found report.
func (t *Tool) Main(ctx context.Context, args []string) error {
	if t.now == nil {
		t.now = time.Now
	}
	if t.df == nil {
		t.df = t.sshDF
	}
	t.human = !slices.ContainsFunc(args, func(a string) bool { return a == "--json" || strings.HasPrefix(a, "--json=") })
	app := oden.New("o-qbt", args, oden.WithBuiltins(), oden.WithStdio(t.Stdin, t.Stdout, t.Stderr), oden.WithEnv(t.Env))
	sh := t.shell()
	t.mount(app, sh)
	app.Default = func(ctx context.Context, args []string) error {
		if len(args) == 0 {
			return t.runShell(ctx, sh, args)
		}
		if words := shortcuts[args[0]]; words != nil {
			return t.runShell(ctx, sh, append(slices.Clone(words), args[1:]...))
		}
		return oden.ErrNotFound
	}
	return app.Run(ctx)
}

// shortcuts are top-level words that stand for a noun and verb.
var shortcuts = map[string][]string{"torrents": {"torrent", "list"}, "categories": {"category", "list"}, "overview": {"torrent", "overview"}}

// mount reuses shared command metadata and keeps human output at execution.
func (t *Tool) mount(app *oden.App, sh *ocli.Shell) {
	ocli.Mount(app, sh)
	for _, entry := range app.Catalog().Entries {
		if entry.Command == nil || entry.Source != "noun" {
			continue
		}
		noun := entry.Name
		for i := range entry.Command.Children {
			command := &entry.Command.Children[i]
			verb := command.Name
			command.Run = func(ctx context.Context, args []string) error {
				return t.runShell(ctx, sh, append([]string{noun, verb}, args...))
			}
		}
	}
}

// runShell runs one command; when the answer has a rendering for a person
// and the caller is one (no --json), that rendering replaces the JSON.
func (t *Tool) runShell(ctx context.Context, sh *ocli.Shell, args []string) error {
	var buf bytes.Buffer
	sh.Stdout, t.text = &buf, nil
	err := sh.Run(ctx, args)
	sh.Stdout = t.Stdout
	if err == nil && t.text != nil {
		_, err = io.WriteString(t.Stdout, *t.text)
		return err
	}
	if _, werr := t.Stdout.Write(buf.Bytes()); err == nil {
		err = werr
	}
	return err
}

// client is the process's API session for c.
func (t *Tool) client(c Config) *Client {
	if t.cl == nil {
		t.cl = newClient(c)
	}
	return t.cl
}

// exec runs an external command (t.run in tests); its combined output.
func (t *Tool) exec(ctx context.Context, name string, args ...string) (string, error) {
	if t.run != nil {
		return t.run(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = t.Env
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%s timed out", name)
	}
	return strings.TrimSpace(string(out)), err
}
