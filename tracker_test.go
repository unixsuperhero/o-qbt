package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTrackerHelpDoesNotCallAPI(t *testing.T) {
	for _, args := range [][]string{{"tracker"}, {"tracker", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newFake(t)
			tool := testTool(t, f.URL, "good", nil)
			out, stderr, err := cli(t, tool, args...)
			if err != nil {
				t.Fatalf("help: %v\n%s", err, stderr)
			}
			for _, want := range []string{"o-qbt tracker", "list", "one torrent's trackers", "summary", "one row per tracker host", "hosts"} {
				if !strings.Contains(out, want) {
					t.Errorf("help missing %q:\n%s", want, out)
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.calls) != 0 {
				t.Errorf("help made API requests: %v", f.calls)
			}
		})
	}
}

func TestTrackerHostStripsPathQueryAndUserInfo(t *testing.T) {
	for in, want := range map[string]string{
		"https://Tracker.Example.org/a1b2c3d4SECRETPASSKEY/announce":           "https://tracker.example.org",
		"https://tracker.example.org/announce.php?passkey=SECRETPASSKEY":       "https://tracker.example.org",
		"http://user:SECRETPASSKEY@private.example.com/SECRETPASSKEY/announce": "http://private.example.com",
		"udp://open.example.net:1337/announce":                                 "udp://open.example.net:1337",
		"SECRETPASSKEY":                                                        "(unparsed)",
		"://bad SECRETPASSKEY":                                                 "(unparsed)",
	} {
		if got := TrackerHost(in); got != want {
			t.Errorf("TrackerHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTrackerListSkipsPseudoEntriesAndHidesURLs(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	out := run(t, tool, "tracker", "list", "--target", "bbb", "--json", "{}")
	if strings.Contains(out, "SECRET") || strings.Contains(out, "**") {
		t.Errorf("tracker list leaks a URL or keeps a pseudo-entry:\n%s", out)
	}
	var rows []struct {
		ID    string      `json:"id"`
		Label string      `json:"label"`
		Data  TrackerData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err, out)
	}
	if len(rows) != 2 || rows[0].ID != "https://tracker.example.org" || rows[0].Label != "https://tracker.example.org  working" ||
		rows[0].Data != (TrackerData{"https://tracker.example.org", "working", 0, 12, 10, 2, ""}) ||
		rows[1].Data.Status != "not working" || rows[1].Data.Msg != "Connection timed out" {
		t.Errorf("rows: %+v", rows)
	}
	if got := run(t, tool, "tracker", "list", "--target", "aaa", "--format", "csv", "--columns", "host,tier"); got != "host,tier\nhttps://tracker.example.org,0\nhttps://tracker.example.org,1\nudp://open.example.net:1337,2\n" {
		t.Errorf("csv:\n%s", got)
	}
	if got := run(t, tool, "tracker", "list", "--target", "aaa", "--json", "{}"); !strings.Contains(got, `"id":"https://tracker.example.org#2"`) {
		t.Errorf("a host listed again is not #2:\n%s", got)
	}
	full := run(t, tool, "tracker", "list", "--target", "bbb", "--full-url", "--format", "table")
	if !strings.Contains(full, "https://Tracker.Example.org/a1b2c3d4SECRETPASSKEY/announce") {
		t.Errorf("--full-url hides the URL:\n%s", full)
	}
	if _, _, err := cli(t, tool, "tracker", "list"); err == nil || !strings.Contains(err.Error(), "needs --target HASH") {
		t.Errorf("no target: %v", err)
	}
}

func TestTrackerSummaryAggregatesByHost(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	out, stderr, err := cli(t, tool, "tracker", "summary", "--json", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "SECRET") {
		t.Errorf("summary leaks a passkey:\n%s", out)
	}
	if !strings.Contains(stderr, "trackers: 3/3 torrents") {
		t.Errorf("no progress line: %q", stderr)
	}
	var hs []HostSummary
	if err := json.Unmarshal([]byte(out), &hs); err != nil {
		t.Fatal(err, out)
	}
	want := []HostSummary{
		{"udp://open.example.net:1337", 3, 0, 3, "Connection timed out"},
		{"https://tracker.example.org", 2, 1, 1, "Unregistered torrent"},
		{"http://private.example.com", 1, 0, 0, ""},
	}
	if len(hs) != len(want) {
		t.Fatalf("summary: %+v", hs)
	}
	for i := range want {
		if hs[i] != want[i] {
			t.Errorf("row %d: %+v, want %+v", i, hs[i], want[i])
		}
	}
	table := run(t, tool, "tracker", "summary", "--format", "table")
	if !strings.HasPrefix(table, "host                         torrents  working  not-working  msg sample\n") {
		t.Errorf("table:\n%s", table)
	}
	if got := run(t, tool, "tracker", "hosts"); got != "http://private.example.com\nhttps://tracker.example.org\nudp://open.example.net:1337\n" {
		t.Errorf("hosts:\n%s", got)
	}
	if got := run(t, tool, "tracker", "hosts", "--filter", "downloading"); got != "https://tracker.example.org\nudp://open.example.net:1337\n" {
		t.Errorf("hosts --filter downloading:\n%s", got)
	}
	if got := run(t, tool, "tracker", "hosts", "--filter", "errored"); got != "" {
		t.Errorf("hosts of no torrents: %q", got)
	}
}

func TestSummarizeTrackersCutsTheMessage(t *testing.T) {
	long := strings.Repeat("x", 80)
	hs := SummarizeTrackers([][]Tracker{{{URL: "https://a.example/k/announce", Status: 4, Msg: long}, {URL: "https://a.example/k2/announce", Status: 4, Msg: "short"}}, {{URL: "https://a.example/k/announce", Status: 4, Msg: long}}})
	if len(hs) != 1 || hs[0].Torrents != 2 || hs[0].NotWorking != 2 || len([]rune(hs[0].Msg)) != 60 || !strings.HasSuffix(hs[0].Msg, "…") {
		t.Errorf("%+v", hs)
	}
}
