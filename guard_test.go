package main

import (
	"os"
	"strings"
	"testing"
)

func TestGuardThresholdMarkerAndResume(t *testing.T) {
	f := newFake(t)
	free := int64(100 * gib)
	tool := testTool(t, f.URL, "good", &free)
	if _, _, err := cli(t, tool, "guard", "check"); err == nil || !strings.Contains(err.Error(), "no baseline") {
		t.Fatalf("check without baseline: %v", err)
	}
	if _, _, err := cli(t, tool, "guard", "rebase"); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		free   int64
		want   string
		pauses int
	}{
		{95 * gib, "free=95.0 baseline=100.0 used=5.0 limit=10.0 action=ok", 0},
		{90 * gib, "action=ok", 0}, // exactly the limit is still ok
		{89 * gib, "free=89.0 baseline=100.0 used=11.0 limit=10.0 action=paused", 1},
		{80 * gib, "action=skipped-already-paused", 1},
	}
	for _, s := range steps {
		free = s.free
		out, _, err := cli(t, tool, "guard", "check")
		if err != nil || out != "2026-10-25T12:30:00-0400 "+strings.TrimPrefix(out, "2026-10-25T12:30:00-0400 ") || !strings.Contains(out, s.want) {
			t.Fatalf("free %d: %v %q", s.free, err, out)
		}
		if n := strings.Count(strings.Join(f.posted(), "|"), "torrents/stop all"); n != s.pauses {
			t.Fatalf("free %d: %d pauses, want %d", s.free, n, s.pauses)
		}
	}
	if m := tool.marker(); m != "2026-10-25T12:30:00-0400" {
		t.Fatalf("marker %q", m)
	}
	out, _, err := cli(t, tool, "guard", "status")
	if err != nil || !strings.Contains(out, "paused    since 2026-10-25T12:30:00-0400") || !strings.Contains(out, "used      20.0 GiB of 10.0 GiB limit") {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if _, _, err := cli(t, tool, "guard", "resume"); err != nil {
		t.Fatal(err)
	}
	if tool.marker() != "" || !strings.HasSuffix(strings.Join(f.posted(), "|"), "torrents/start all") {
		t.Fatalf("resume left marker %q, posted %v", tool.marker(), f.posted())
	}
	out, _, _ = cli(t, tool, "guard", "log", "3")
	if lines := strings.Split(strings.TrimSpace(out), "\n"); len(lines) != 3 || !strings.HasSuffix(lines[2], "action=resumed") {
		t.Fatalf("log: %q", out)
	}
	free = 50 * gib
	if out, _, _ := cli(t, tool, "guard", "check", "--limit", "60G"); !strings.Contains(out, "limit=60.0 action=ok") {
		t.Fatalf("--limit: %q", out)
	}
}

func TestGuardWithoutPasswordLogsNoCredentials(t *testing.T) {
	f := newFake(t)
	free := int64(50 * gib)
	tool := testTool(t, f.URL, "", &free)
	if _, _, err := cli(t, tool, "guard", "rebase", "--bytes", "107374182400"); err != nil {
		t.Fatal(err)
	}
	out, _, err := cli(t, tool, "guard", "check")
	if err != nil || !strings.HasSuffix(strings.TrimSpace(out), "used=50.0 limit=10.0 action=error:no-credentials") {
		t.Fatalf("%v %q", err, out)
	}
	if f.requests() != 0 || tool.marker() != "" {
		t.Fatalf("requests %d marker %q", f.requests(), tool.marker())
	}
	data, _ := os.ReadFile(tool.guardLog())
	if !strings.Contains(string(data), "action=rebase\n") || !strings.Contains(string(data), "action=error:no-credentials\n") {
		t.Fatalf("log:\n%s", data)
	}
}

func TestParseSizeAndDF(t *testing.T) {
	for in, want := range map[string]int64{"10G": 10 << 30, "10GiB": 10 << 30, "512m": 512 << 20, "1024": 1024, "1.5T": 3 << 39} {
		if got, err := parseSize(in); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := parseSize("ten"); err == nil {
		t.Error("parseSize(ten) ok")
	}
	if n, err := parseDF("       Avail\n619700166656\n"); err != nil || n != 619700166656 {
		t.Errorf("parseDF = %d, %v", n, err)
	}
}

func TestGuardPlist(t *testing.T) {
	tool := testTool(t, "http://127.0.0.1:1", "", nil)
	p := tool.GuardPlist()
	for _, want := range []string{"<string>" + GuardLabel + "</string>", "<string>" + tool.Home + "/bin/o-qbt</string>\n    <string>guard</string>\n    <string>check</string>", "<integer>300</integer>", "<key>RunAtLoad</key>\n  <true/>"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
}
