package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestGuardThresholdMarkerAndResume(t *testing.T) {
	f := newFake(t)
	free := int64(100 * gib)
	tool := testTool(t, f.URL, "good", &free)
	if _, _, err := cli(t, tool, "config", "set", "guard.mode", "allowance"); err != nil {
		t.Fatal(err)
	}
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
	if err != nil || !strings.Contains(out, "paused    by the guard since 2026-10-25T12:30:00-0400") || !strings.Contains(out, "used      20.0 GiB of 10.0 GiB limit") {
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

func TestGuardFloor(t *testing.T) {
	f := newFake(t)
	free := int64(30 * gib)
	tool := testTool(t, f.URL, "good", &free)
	steps := []struct {
		free   int64
		action string
		marker bool
		posted string
	}{
		{30 * gib, "ok", false, ""},
		{4 * gib, "paused", true, "torrents/stop all"},     // below min
		{5 * gib, "skipped-already-paused", true, ""},      // exactly min, still paused
		{20 * gib, "skipped-already-paused", true, ""},     // between: no change
		{21 * gib, "resumed", false, "torrents/start all"}, // above resume, the guard paused
		{15 * gib, "ok", false, ""},                        // between, not paused: no change
		{5 * gib, "ok", false, ""},                         // exactly min is ok
	}
	for _, s := range steps {
		free = s.free
		before := len(f.posted())
		out, _, err := cli(t, tool, "guard", "check")
		want := fmt.Sprintf("2026-10-25T12:30:00-0400 mode=floor min=5.0 resume=20.0 free=%.1f baseline=0.0 used=0.0 limit=10.0 action=%s\n", float64(s.free)/gib, s.action)
		if err != nil || out != want {
			t.Fatalf("free %d: %v\n%q\nwant %q", s.free, err, out, want)
		}
		if (tool.marker() != "") != s.marker || strings.Join(f.posted()[before:], "|") != s.posted {
			t.Fatalf("free %d: marker %q posted %v", s.free, tool.marker(), f.posted()[before:])
		}
	}
	// a pause the guard did not make is never resumed by it
	free = 100 * gib
	before := len(f.posted())
	if out, _, _ := cli(t, tool, "guard", "check"); !strings.HasSuffix(out, "action=ok\n") || len(f.posted()) != before {
		t.Fatalf("resumed what it did not pause: %q", out)
	}
	if out, _, _ := cli(t, tool, "guard", "check", "--min-free", "120GiB", "--resume-above", "200G"); !strings.Contains(out, "min=120.0 resume=200.0 free=100.0") || !strings.HasSuffix(out, "action=paused\n") {
		t.Fatalf("flags: %q", out)
	}
	if _, _, err := cli(t, tool, "config", "set", "guard.minFree", "25G"); err == nil || !strings.Contains(err.Error(), "must be above minFree") {
		t.Fatalf("minFree above resumeAbove: %v", err)
	}
	if _, _, err := cli(t, tool, "config", "set", "guard.mode", "panic"); err == nil {
		t.Fatal("bad mode accepted")
	}
	for _, kv := range [][2]string{{"guard.resumeAbove", "150G"}, {"guard.minFree", "50G"}} {
		if _, _, err := cli(t, tool, "config", "set", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	out, _, err := cli(t, tool, "guard", "status")
	for _, want := range []string{"mode      floor", "min free  50.0 GiB (pause below)", "resume    above 150.0 GiB when the guard paused", "free      100.0 GiB on nas:/volume1", "paused    by the guard since 2026-10-25T12:30:00-0400"} {
		if err != nil || !strings.Contains(out, want) {
			t.Errorf("status lacks %q: %v\n%s", want, err, out)
		}
	}
	if strings.Contains(out, "\nbaseline ") {
		t.Errorf("floor status shows the baseline:\n%s", out)
	}
	out, _, err = cli(t, tool, "guard", "rebase")
	if err != nil || !strings.Contains(out, "action=rebase\nnote: the guard is in floor mode (pause below 50.0 GiB); the baseline only matters in allowance mode") {
		t.Fatalf("rebase in floor mode: %v %q", err, out)
	}
	out, _, _ = cli(t, tool, "config", "show")
	if !strings.Contains(out, "guard     mode floor, minFree 50.0 GiB, resumeAbove 150.0 GiB") {
		t.Errorf("config show:\n%s", out)
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
