package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// golden compares got with testdata/NAME (rewritten with -update).
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s differs (go test -update):\n%s", path, got)
	}
}

func run(t *testing.T, tool *Tool, args ...string) string {
	t.Helper()
	out, _, err := cli(t, tool, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return out
}

func TestListQueryIsPassedThrough(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"torrent", "list"}, ""},
		{[]string{"torrent", "list", "--filter", "seeding", "--category", "tv", "--tag", "hd"}, "category=tv&filter=seeding&tag=hd"},
		{[]string{"torrent", "list", "--category", ""}, "category="},
		{[]string{"torrent", "list", "--sort", "completed_on", "--reverse", "--limit", "2"}, "limit=2&reverse=true&sort=completion_on"},
		{[]string{"torrent", "export", "--filter", "active", "--out", filepath.Join(t.TempDir(), "x.csv")}, "filter=active"},
		{[]string{"torrent", "summary", "--filter", "stalled_downloading"}, "filter=stalled_downloading"},
	}
	for _, p := range torrentPresets {
		cases = append(cases, struct {
			args []string
			want string
		}{[]string{"torrent", p.Verb, "--limit", "1"}, "filter=" + p.Filter + "&limit=1"})
	}
	for _, tc := range cases {
		run(t, tool, tc.args...)
		if got := f.queries[len(f.queries)-1]; got != tc.want {
			t.Errorf("%v sent %q, want %q", tc.args, got, tc.want)
		}
	}
	if _, _, err := cli(t, tool, "torrent", "list", "--filter", "nope"); err == nil || !strings.Contains(err.Error(), "filter must be one of all, downloading") {
		t.Errorf("bad filter: %v", err)
	}
	if _, _, err := cli(t, tool, "torrent", "list", "--sort", "colour"); err == nil {
		t.Error("bad sort accepted")
	}
	if _, _, err := cli(t, tool, "torrent", "export"); err == nil || !strings.Contains(err.Error(), "needs --out FILE") {
		t.Errorf("export without --out: %v", err)
	}
}

func TestSortLimitReverseAndPresets(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "aaa,ccc,bbb"}, // by name, here
		{[]string{"--reverse"}, "bbb,ccc,aaa"},
		{[]string{"--sort", "size"}, "bbb,aaa,ccc"},
		{[]string{"--sort", "size", "--reverse", "--limit", "2"}, "ccc,aaa"},
		{[]string{"--sort", "added_on"}, "ccc,bbb,aaa"},
		{[]string{"--filter", "downloading"}, "aaa"},
		{[]string{"--tag", "new"}, "ccc"},
	} {
		args := append([]string{"torrent", "list", "--format", "csv", "--columns", "hash"}, tc.args...)
		got := strings.Join(strings.Fields(strings.TrimPrefix(run(t, tool, args...), "hash\n")), ",")
		if got != tc.want {
			t.Errorf("%v: %s, want %s", tc.args, got, tc.want)
		}
	}
	for verb, want := range map[string]string{"active": "aaa,bbb", "seeding": "bbb", "downloading": "aaa", "paused": "ccc", "errored": "", "stalled": "bbb"} {
		got := strings.Join(strings.Fields(strings.TrimPrefix(run(t, tool, "torrent", verb, "--format", "tsv", "--columns", "hash"), "hash\n")), ",")
		if got != want {
			t.Errorf("torrent %s: %q, want %q", verb, got, want)
		}
	}
}

func TestFormatsGolden(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	for _, format := range Formats {
		golden(t, "formats/torrent."+format, run(t, tool, "torrent", "list", "--format", format))
	}
	golden(t, "formats/torrent-columns.csv", run(t, tool, "torrent", "list", "--format", "csv", "--columns", "name,tags,ratio,added_on"))
	golden(t, "formats/torrent-columns.table", run(t, tool, "torrent", "list", "--format", "table", "--columns", "name,upspeed,ratio,added_on,completed_on"))
	golden(t, "formats/category.toon", run(t, tool, "category", "list", "--format", "toon"))
	golden(t, "formats/transfer.md", run(t, tool, "transfer", "info", "--format", "md"))
	golden(t, "formats/app.csv", run(t, tool, "app", "info", "--format", "csv", "--columns", "version,webapi_version,free_space_on_disk"))
	if _, _, err := cli(t, tool, "torrent", "list", "--format", "csv", "--columns", "name,colour"); err == nil || !strings.Contains(err.Error(), `torrent has no column "colour" (columns: hash,name,state,progress,size,dlspeed,eta,upspeed,ratio`) {
		t.Errorf("unknown column: %v", err)
	}
}

func TestDefaultFormatFollowsTheTerminal(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	if got := run(t, tool, "torrent", "list"); got != run(t, tool, "torrent", "list", "--format", "json") {
		t.Errorf("no terminal is not json:\n%s", got)
	}
	if got := run(t, tool, "torrents", "--json", "{}"); !strings.Contains(got, `"label":"downloading  42.2%  alpha"`) {
		t.Errorf("--json is not the rows:\n%s", got)
	}
	if got := run(t, tool, "torrents", "--json", `{"format":"toon"}`); !strings.HasPrefix(got, "torrent[3]{") {
		t.Errorf("--json with format:\n%s", got)
	}
	tool.TTY = true
	if got := run(t, tool, "torrent", "list"); got != run(t, tool, "torrent", "list", "--format", "table") || !strings.HasPrefix(got, "hash ") {
		t.Errorf("a terminal is not table:\n%s", got)
	}
}

func TestOutWritesTheFile(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	dir := t.TempDir()
	for _, tc := range []struct{ args []string }{
		{[]string{"torrent", "list", "--out", filepath.Join(dir, "a.md")}},
		{[]string{"torrent", "export", "--format", "toon", "--out", filepath.Join(dir, "a.txt")}},
		{[]string{"torrent", "export", "--out", filepath.Join(dir, "a.out")}},
	} {
		out := run(t, tool, tc.args...)
		path := tc.args[len(tc.args)-1]
		want := map[string]string{".md": "md", ".txt": "toon", ".out": "json"}[filepath.Ext(path)]
		if out != "wrote 3 torrent rows to "+path+" ("+want+")\n" {
			t.Errorf("%v printed %q", tc.args, out)
		}
		data, _ := os.ReadFile(path)
		if string(data) != run(t, tool, "torrent", "list", "--format", want) {
			t.Errorf("%s holds:\n%s", path, data)
		}
	}
	out := run(t, tool, "torrent", "export", "--out", filepath.Join(dir, "b.csv"), "--json", "{}")
	if !strings.Contains(out, `"message": "wrote 3 torrent rows to `) {
		t.Errorf("--json export: %s", out)
	}
}

func TestSummaryAndOverviewGolden(t *testing.T) {
	f := newFake(t)
	free := int64(100 * gib)
	tool := testTool(t, f.URL, "good", &free)
	golden(t, "summary.table", run(t, tool, "torrent", "summary", "--format", "table"))
	golden(t, "summary.json", run(t, tool, "torrent", "summary"))
	golden(t, "overview.table", run(t, tool, "overview", "--format", "table"))
	if err := tool.writeBaseline(628000000000 + 2*gib); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(tool.markerPath(), []byte("2026-10-25T12:00:00-0400\n"), 0o600)
	golden(t, "overview-guard.table", run(t, tool, "torrent", "overview", "--format", "table"))
	golden(t, "overview.json", run(t, tool, "torrent", "overview"))
	golden(t, "overview.md", run(t, tool, "torrent", "overview", "--format", "md"))
}
