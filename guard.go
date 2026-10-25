package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The guard pauses every torrent once the NAS volume's free space has
// dropped more than a limit below a stored baseline. Its state is in the
// data dir ($XDG_DATA_HOME/o-qbt, else ~/.local/share/o-qbt):
//
//	baseline   free bytes when the guard was last rebased
//	paused     the marker: the time guard check paused everything
//	guard.log  one line per check, rebase and resume
//
// guard install runs guard check every 300 s from a launchd agent.

// GuardLabel is the launchd label.
const GuardLabel = "com.unixsuperhero.o-qbt-guard"

// GuardInterval is the agent's StartInterval in seconds.
const GuardInterval = 300

// DefaultLimit is how far below the baseline free space may drop.
const DefaultLimit int64 = 10 << 30

const gib = 1 << 30

// DataDir holds the guard's baseline, marker and log.
func (t *Tool) DataDir() string {
	base := t.getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(t.Home, ".local", "share")
	}
	return filepath.Join(base, "o-qbt")
}

func (t *Tool) baselinePath() string { return filepath.Join(t.DataDir(), "baseline") }
func (t *Tool) markerPath() string   { return filepath.Join(t.DataDir(), "paused") }
func (t *Tool) guardLog() string     { return filepath.Join(t.DataDir(), "guard.log") }

// GuardLine is one guard event, the shape of a guard.log line:
//
//	2026-10-25T12:30:00-0400 free=577.1 baseline=584.8 used=7.7 limit=10.0 action=ok
type GuardLine struct {
	Time     string `json:"time"`
	Free     int64  `json:"free"`
	Baseline int64  `json:"baseline"`
	Used     int64  `json:"used"`
	Limit    int64  `json:"limit"`
	Action   string `json:"action"`
}

func (g GuardLine) String() string {
	return fmt.Sprintf("%s free=%.1f baseline=%.1f used=%.1f limit=%.1f action=%s", g.Time,
		float64(g.Free)/gib, float64(g.Baseline)/gib, float64(g.Used)/gib, float64(g.Limit)/gib, g.Action)
}

func (g GuardLine) Text() string { return g.String() + "\n" }

// logLine appends g to guard.log.
func (t *Tool) logLine(g GuardLine) (GuardLine, error) {
	if err := os.MkdirAll(t.DataDir(), 0o700); err != nil {
		return g, err
	}
	f, err := os.OpenFile(t.guardLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return g, err
	}
	_, err = fmt.Fprintln(f, g.String())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return g, err
}

func (t *Tool) stamp() string { return t.now().Format("2006-01-02T15:04:05-0700") }

// readBaseline is the stored baseline; ok is false without one.
func (t *Tool) readBaseline() (int64, bool, error) {
	data, err := os.ReadFile(t.baselinePath())
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s: %w", t.baselinePath(), err)
	}
	return n, true, nil
}

func (t *Tool) writeBaseline(n int64) error {
	if err := os.MkdirAll(t.DataDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(t.baselinePath(), []byte(strconv.FormatInt(n, 10)+"\n"), 0o600)
}

// marker is the paused marker's contents, "" when absent.
func (t *Tool) marker() string {
	data, err := os.ReadFile(t.markerPath())
	if err != nil {
		return ""
	}
	if s := strings.TrimSpace(string(data)); s != "" {
		return s
	}
	return "yes"
}

// sshDF is the volume's available bytes, from df on the ssh host.
func (t *Tool) sshDF(ctx context.Context, c Config) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := t.exec(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", c.SSH, "df", "-B1", "--output=avail", c.Volume)
	if err != nil {
		return 0, fmt.Errorf("ssh %s df %s: %w: %s", c.SSH, c.Volume, err, oneLine(out))
	}
	return parseDF(out)
}

// parseDF reads df --output=avail: a header, then one number.
func parseDF(out string) (int64, error) {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, errors.New("df printed nothing")
	}
	n, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("df: not a byte count: %q", oneLine(out))
	}
	return n, nil
}

// parseSize is a byte count: N, or N with K/M/G/T (binary; KiB, GB, ... too).
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	m := regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)\s*([kmgt]?)(?:i?b)?$`).FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("not a size: %q (bytes, or a number with K, M, G or T)", s)
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	shift := map[string]uint{"": 0, "k": 10, "m": 20, "g": 30, "t": 40}[strings.ToLower(m[2])]
	return int64(f * float64(int64(1)<<shift)), nil
}

// GuardCheck is the monitor step: pause everything once the volume's free
// space is more than lim below the baseline. In order: no baseline is an
// error; the df failing is an error; a marker skips (idempotent: one pause
// until guard resume or rebase); no password logs error:no-credentials and
// succeeds, so the agent keeps running and the log says what is missing;
// within the limit logs ok; else pause all, write the marker, log paused.
func (t *Tool) GuardCheck(ctx context.Context, lim int64) (GuardLine, error) {
	c, _, _, err := t.config()
	if err != nil {
		return GuardLine{}, err
	}
	g := GuardLine{Time: t.stamp(), Limit: lim}
	base, ok, err := t.readBaseline()
	if err != nil {
		return g, err
	}
	if !ok {
		g.Action = "error:no-baseline"
		t.logLine(g)
		return g, errors.New("no baseline: run o qbt guard rebase")
	}
	g.Baseline = base
	if g.Free, err = t.df(ctx, c); err != nil {
		g.Action = "error:df"
		t.logLine(g)
		return g, err
	}
	g.Used = base - g.Free
	switch {
	case t.marker() != "":
		g.Action = "skipped-already-paused"
	case c.Pass == "":
		g.Action = "error:no-credentials"
	case g.Used <= lim:
		g.Action = "ok"
	default:
		if _, err := t.client(c).Pause(ctx, "all"); err != nil {
			g.Action = "error:pause"
			t.logLine(g)
			return g, err
		}
		if err := os.WriteFile(t.markerPath(), []byte(g.Time+"\n"), 0o600); err != nil {
			return g, err
		}
		g.Action = "paused"
	}
	return t.logLine(g)
}

// GuardRebase stores bytes as the baseline (the current free space when
// bytes is 0), clears the marker and logs rebase.
func (t *Tool) GuardRebase(ctx context.Context, bytes, lim int64) (GuardLine, error) {
	c, _, _, err := t.config()
	if err != nil {
		return GuardLine{}, err
	}
	g := GuardLine{Time: t.stamp(), Limit: lim, Action: "rebase"}
	if g.Free, err = t.df(ctx, c); err != nil {
		if bytes == 0 {
			return g, err
		}
		g.Free = bytes
	}
	g.Baseline = g.Free
	if bytes > 0 {
		g.Baseline = bytes
	}
	g.Used = g.Baseline - g.Free
	if err := t.writeBaseline(g.Baseline); err != nil {
		return g, err
	}
	if err := os.Remove(t.markerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return g, err
	}
	return t.logLine(g)
}

// GuardResume resumes every torrent, clears the marker and logs resumed.
func (t *Tool) GuardResume(ctx context.Context, lim int64) (GuardLine, error) {
	c, _, _, err := t.config()
	if err != nil {
		return GuardLine{}, err
	}
	g := GuardLine{Time: t.stamp(), Limit: lim}
	g.Baseline, _, _ = t.readBaseline()
	if free, err := t.df(ctx, c); err == nil {
		g.Free, g.Used = free, g.Baseline-free
	}
	if _, err := t.client(c).Resume(ctx, "all"); err != nil {
		g.Action = "error:resume"
		t.logLine(g)
		return g, err
	}
	if err := os.Remove(t.markerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return g, err
	}
	g.Action = "resumed"
	return t.logLine(g)
}

// GuardStatus is guard status's answer.
type GuardStatus struct {
	Launchd  string `json:"launchd"`
	LastLine string `json:"last_line"`
	Target   string `json:"target"`
	Free     int64  `json:"free"`
	DFError  string `json:"df_error,omitempty"`
	Baseline int64  `json:"baseline"`
	Used     int64  `json:"used"`
	Limit    int64  `json:"limit"`
	Paused   string `json:"paused"`
	PassSet  bool   `json:"pass_set"`
	Log      string `json:"log"`
}

func (s GuardStatus) Text() string {
	free := bytesH(s.Free)
	if s.DFError != "" {
		free = "unknown (" + s.DFError + ")"
	}
	base := bytesH(s.Baseline)
	if s.Baseline == 0 {
		base = "none (o qbt guard rebase)"
	}
	pass := "set"
	if !s.PassSet {
		pass = "not set: guard check cannot pause (o qbt config set pass)"
	}
	return table([][2]string{
		{"launchd", s.Launchd}, {"last", s.LastLine}, {"free", free + " on " + s.Target}, {"baseline", base},
		{"used", fmt.Sprintf("%s of %s limit", bytesH(s.Used), bytesH(s.Limit))}, {"paused", s.Paused},
		{"pass", pass}, {"log", s.Log},
	})
}

// GuardStatusOf reports the agent, the last log line, free space against
// the baseline and the marker. A df failure is reported, not an error.
func (t *Tool) GuardStatusOf(ctx context.Context, lim int64) (GuardStatus, error) {
	c, _, _, err := t.config()
	if err != nil {
		return GuardStatus{}, err
	}
	s := GuardStatus{Target: c.SSH + ":" + c.Volume, Limit: lim, PassSet: c.Pass != "", Log: t.guardLog(), Paused: "no"}
	if m := t.marker(); m != "" {
		s.Paused = "since " + m + " (o qbt guard resume)"
	}
	if s.Baseline, _, err = t.readBaseline(); err != nil {
		return s, err
	}
	if s.Free, err = t.df(ctx, c); err != nil {
		s.DFError = err.Error()
	} else {
		s.Used = s.Baseline - s.Free
	}
	if s.LastLine = lastLines(t.guardLog(), 1); s.LastLine == "" {
		s.LastLine = "none"
	}
	if out, err := t.exec(ctx, "launchctl", "print", t.service()); err == nil {
		s.Launchd = "loaded as " + t.service() + " (" + printSummary(out) + ")"
	} else {
		s.Launchd = "not loaded (o qbt guard install)"
	}
	return s, nil
}

// LogTail is guard log's answer.
type LogTail struct {
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
}

func (l LogTail) Text() string {
	if len(l.Lines) == 0 {
		return "no guard log yet (" + l.Path + ")\n"
	}
	return strings.Join(l.Lines, "\n") + "\n"
}

// GuardLogTail is the last n lines of guard.log.
func (t *Tool) GuardLogTail(n int) LogTail {
	s := lastLines(t.guardLog(), n)
	l := LogTail{Path: t.guardLog(), Lines: []string{}}
	if s != "" {
		l.Lines = strings.Split(s, "\n")
	}
	return l
}

// lastLines is the last n non-blank lines of path, joined by newlines.
func lastLines(path string, n int) string {
	data, err := os.ReadFile(path)
	if err != nil || n <= 0 {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// The launchd agent.

func (t *Tool) domain() string  { return "gui/" + strconv.Itoa(t.UID) }
func (t *Tool) service() string { return t.domain() + "/" + GuardLabel }
func (t *Tool) PlistPath() string {
	return filepath.Join(t.Home, "Library", "LaunchAgents", GuardLabel+".plist")
}
func (t *Tool) LogDir() string { return filepath.Join(t.Home, "Library", "Logs", "o-qbt") }

// BinPath is the binary the agent runs: ~/bin/o-qbt.
func (t *Tool) BinPath() string { return filepath.Join(t.Home, "bin", "o-qbt") }

// GuardPlist is the agent: o-qbt guard check every GuardInterval seconds
// and once at load.
func (t *Tool) GuardPlist() string {
	esc := html.EscapeString
	path := strings.Join([]string{filepath.Join(t.Home, "bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + GuardLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + esc(t.BinPath()) + `</string>
    <string>guard</string>
    <string>check</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>` + esc(path) + `</string>
    <key>HOME</key>
    <string>` + esc(t.Home) + `</string>
  </dict>
  <key>StartInterval</key>
  <integer>` + strconv.Itoa(GuardInterval) + `</integer>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>` + esc(filepath.Join(t.LogDir(), "guard.out.log")) + `</string>
  <key>StandardErrorPath</key>
  <string>` + esc(filepath.Join(t.LogDir(), "guard.err.log")) + `</string>
</dict>
</plist>
`
}

// Lines is a verb's answer for a person: lines of what it did.
type Lines []string

func (l Lines) Text() string { return strings.Join(l, "\n") + "\n" }

// GuardInstall writes the plist, lints it and bootstraps it in gui/UID
// (bootout first when loaded); idempotent. The agent runs check at load.
func (t *Tool) GuardInstall(ctx context.Context) (Lines, error) {
	var out Lines
	if info, err := os.Stat(t.BinPath()); err != nil || info.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("%s is missing or not executable: go build -o %s . first", t.BinPath(), t.BinPath())
	}
	for _, dir := range []string{t.LogDir(), t.DataDir(), filepath.Dir(t.PlistPath())} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(t.PlistPath(), []byte(t.GuardPlist()), 0o644); err != nil {
		return nil, err
	}
	out = append(out, fmt.Sprintf("wrote %s (guard check every %ds)", t.PlistPath(), GuardInterval))
	if o, err := t.exec(ctx, "plutil", "-lint", t.PlistPath()); err != nil {
		return out, fmt.Errorf("plutil -lint: %w: %s", err, o)
	}
	out = append(out, "plutil -lint: OK")
	booted := false
	if _, err := t.exec(ctx, "launchctl", "print", t.service()); err == nil {
		if o, err := t.exec(ctx, "launchctl", "bootout", t.service()); err != nil {
			return out, fmt.Errorf("launchctl bootout %s: %w: %s", t.service(), err, o)
		}
		booted = true
		out = append(out, "launchctl bootout "+t.service()+": was loaded, unloaded")
	}
	// Right after a bootout launchd can refuse the bootstrap for a moment
	// (error 5); try again briefly.
	var o string
	var err error
	for try := 0; ; try++ {
		if o, err = t.exec(ctx, "launchctl", "bootstrap", t.domain(), t.PlistPath()); err == nil || !booted || try == 9 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		return out, fmt.Errorf("launchctl bootstrap %s %s: %w: %s", t.domain(), t.PlistPath(), err, o)
	}
	out = append(out, "launchctl bootstrap "+t.domain()+" "+t.PlistPath()+": ok")
	if o, err = t.exec(ctx, "launchctl", "print", t.service()); err != nil {
		return out, fmt.Errorf("launchctl print %s: %w: %s", t.service(), err, o)
	}
	return append(out, "launchctl print "+t.service()+": loaded ("+printSummary(o)+")"), nil
}

// GuardUninstall boots the agent out and removes its plist; the data
// dir (baseline, marker, log) stays.
func (t *Tool) GuardUninstall(ctx context.Context) (Lines, error) {
	var out Lines
	if _, err := t.exec(ctx, "launchctl", "print", t.service()); err == nil {
		if o, err := t.exec(ctx, "launchctl", "bootout", t.service()); err != nil {
			return out, fmt.Errorf("launchctl bootout %s: %w: %s", t.service(), err, o)
		}
		out = append(out, "launchctl bootout "+t.service()+": unloaded")
	} else {
		out = append(out, "launchctl bootout "+t.service()+": not loaded")
	}
	switch err := os.Remove(t.PlistPath()); {
	case err == nil:
		out = append(out, "removed "+t.PlistPath())
	case errors.Is(err, os.ErrNotExist):
		out = append(out, "absent "+t.PlistPath())
	default:
		return out, err
	}
	return append(out, "kept "+t.DataDir()+" (baseline, marker, guard.log)"), nil
}

// printSummary is the state, runs and last exit code of a launchctl print
// answer, comma separated.
func printSummary(out string) string {
	var parts []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		key, _, _ := strings.Cut(strings.TrimSpace(line), " = ")
		switch key {
		case "state", "runs", "last exit code":
			parts = append(parts, strings.TrimSpace(line))
		}
	}
	if len(parts) == 0 {
		return "no state reported"
	}
	return strings.Join(parts, ", ")
}
