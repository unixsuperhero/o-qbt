package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/oden"
	"time"

	ocli "github.com/unixsuperhero/ochazuke/cli"
)

var update = flag.Bool("update", false, "rewrite testdata/catalog.golden")

func TestAppInfoLogsInOnceAndPrintsTable(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	tool.TTY = true
	out, _, err := cli(t, tool, "app", "info")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"login      ok", "user       admin", "version    v5.0.4", "webapi     2.11.2", "qt 6.7.2", "download   2.0 MiB/s", "free disk  584.9 GiB"} {
		if !strings.Contains(out, want) {
			t.Errorf("app info lacks %q:\n%s", want, out)
		}
	}
	if f.logins != 1 {
		t.Errorf("logins = %d, want 1", f.logins)
	}
	out, _, err = cli(t, tool, "app", "info", "--json", "{}")
	if err != nil {
		t.Fatal(err)
	}
	var info Info
	if err := json.Unmarshal([]byte(out), &info); err != nil || info.Login != "ok" || info.FreeSpace != 628000000000 {
		t.Fatalf("--json: %v %+v\n%s", err, info, out)
	}
}

func TestLoginRefusedIsOneAttempt(t *testing.T) {
	for _, tc := range []struct {
		name string
		ban  bool
		want string
	}{{"wrong password", false, "HTTP 200: Fails."}, {"403", true, "HTTP 403"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.ban = tc.ban
			tool := testTool(t, f.URL, "bad", nil)
			_, _, err := cli(t, tool, "app", "state") // two lists, one process
			if err == nil || !strings.Contains(err.Error(), "login refused: check user/pass; qBittorrent bans an IP after repeated failures, so do not retry blindly") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
			if f.logins != 1 || f.requests() != 1 {
				t.Fatalf("logins %d, requests %d: want one attempt and nothing else", f.logins, f.requests())
			}
		})
	}
}

func TestNoCredentialsSendsNothing(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "", nil)
	_, _, err := cli(t, tool, "app", "info")
	if err == nil || !strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("err = %v", err)
	}
	if f.requests() != 0 {
		t.Fatalf("%d requests without a password", f.requests())
	}
}

func TestTimeoutIsAClearError(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer slow.Close()
	c := newClient(Config{URL: slow.URL, User: "admin", Pass: "p"})
	c.HTTP.Timeout = 50 * time.Millisecond
	_, err := AppInfo(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "did not answer within 50ms (timeout)") {
		t.Fatalf("err = %v", err)
	}
	if _, err2 := AppInfo(context.Background(), c); err2 != err {
		t.Fatalf("second call retried the login: %v", err2)
	}
}

func TestTorrentRows(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	out, _, err := cli(t, tool, "torrents", "--json", "{}")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID    string      `json:"id"`
		Label string      `json:"label"`
		Data  TorrentData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err, out)
	}
	if len(rows) != 3 || rows[0].ID != "aaa" || rows[0].Label != "downloading  42.2%  alpha" || rows[1].Label != "pausedDL  50.0%  Movie, The \"Cut\"" || rows[2].Label != "stalledUP  100.0%  Zeta" {
		t.Fatalf("rows: %+v", rows)
	}
	if d := rows[2].Data; d.CompletedOn != 20 || d.Category != "tv" || d.SavePath != "/downloads/tv" || d.Size != 2048 {
		t.Fatalf("data: %+v", d)
	}
	out, _, err = cli(t, tool, "app", "state", "--json", "{}")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"noun":"torrent","id":"aaa","label":"downloading  42.2%  alpha"}
{"noun":"torrent","id":"ccc","label":"pausedDL  50.0%  Movie, The \"Cut\""}
{"noun":"torrent","id":"bbb","label":"stalledUP  100.0%  Zeta"}
{"noun":"category","id":"category/movies","label":"movies  "}
{"noun":"category","id":"category/tv","label":"tv  /downloads/tv"}
`
	if out != want {
		t.Fatalf("state:\n%s", out)
	}
	out, _, err = cli(t, tool, "torrent", "show", "--target", "bbb")
	if err != nil || !strings.Contains(out, `"comment": "c"`) {
		t.Fatalf("show: %v\n%s", err, out)
	}
	if _, _, err = cli(t, tool, "torrent", "show", "--target", "zzz"); err == nil || !strings.Contains(err.Error(), "no torrent with hash zzz") {
		t.Fatalf("unknown hash: %v", err)
	}
}

func TestPauseFallsBackToV4AndResume(t *testing.T) {
	for _, v4 := range []bool{false, true} {
		f := newFake(t)
		f.v4 = v4
		tool := testTool(t, f.URL, "good", nil)
		if _, _, err := cli(t, tool, "torrent", "pause", "--target", "aaa"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := cli(t, tool, "torrent", "resume", "--all"); err != nil {
			t.Fatal(err)
		}
		want := "torrents/stop aaa|torrents/start all"
		if v4 {
			want = "torrents/stop aaa|torrents/pause aaa|torrents/start all|torrents/resume all"
		}
		if got := strings.Join(f.posted(), "|"); got != want {
			t.Errorf("v4=%v posted %s, want %s", v4, got, want)
		}
	}
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	if _, _, err := cli(t, tool, "torrent", "pause"); err == nil || !strings.Contains(err.Error(), "--target HASH") {
		t.Fatalf("pause with neither: %v", err)
	}
}

func TestDeleteAsksFirst(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	tool.Stdin = strings.NewReader("n\n")
	_, stderr, err := cli(t, tool, "torrent", "delete", "--target", "aaa")
	if err == nil || !strings.Contains(stderr, "Delete torrent downloading  42.2%  alpha?") || len(f.removed) != 0 {
		t.Fatalf("declined delete: %v %q %v", err, stderr, f.removed)
	}
	if _, _, err := cli(t, tool, "torrent", "delete", "--target", "aaa", "--files", "--yes"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.removed, ",") != "aaa files=true" {
		t.Fatalf("removed %v", f.removed)
	}
}

func TestDeleteSelection(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	if _, _, err := cli(t, tool, "torrent", "delete", "--state", "stalledUP", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli(t, tool, "torrent", "delete", "--hashes", "aaa,ccc", "--yes"); err != nil {
		t.Fatal(err)
	}
	old := deleteBatch
	deleteBatch = 2
	t.Cleanup(func() { deleteBatch = old })
	_, stderr, err := cli(t, tool, "torrent", "delete", "--filter", "all", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "deleted 2/3") || !strings.Contains(stderr, "deleted 3/3") {
		t.Errorf("progress %q", stderr)
	}
	if got := strings.Join(f.removed, ","); got != "bbb files=false,aaa|ccc files=false,aaa|ccc files=false,bbb files=false" {
		t.Fatalf("removed %v", f.removed)
	}
	if _, _, err := cli(t, tool, "torrent", "delete", "--target", "aaa", "--state", "x", "--yes"); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatal("--target with a selection should fail")
	}
}

func TestTransferLimitAndPrefs(t *testing.T) {
	f := newFake(t)
	tool := testTool(t, f.URL, "good", nil)
	out, _, err := cli(t, tool, "transfer", "limit", "--dl", "0", "--up", "1024")
	if err != nil || !strings.Contains(out, "connection  connected") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.Join(f.posted(), "|"); got != "transfer/setDownloadLimit|transfer/setUploadLimit" {
		t.Fatalf("posted %s", got)
	}
	out, _, err = cli(t, tool, "app", "prefs")
	if err != nil || !strings.Contains(out, `"max_active_downloads": 3`) || strings.Contains(out, "web_ui_password") {
		t.Fatalf("prefs: %v\n%s", err, out)
	}
}

func TestConfigRoundTripNeverPrintsPassword(t *testing.T) {
	tool := testTool(t, "http://127.0.0.1:1", "", nil)
	out, _, err := cli(t, tool, "config", "init")
	path := tool.ConfigPath()
	if err != nil || !strings.HasPrefix(out, path+"\n") {
		t.Fatalf("init: %v %q", err, out)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if _, _, err := cli(t, tool, "config", "set", "pass", "hunter2"); err == nil {
		t.Fatal("pass from argv accepted")
	}
	tool.Stdin = strings.NewReader("hunter2\n")
	out, _, err = cli(t, tool, "config", "set", "pass", "--stdin")
	if err != nil || strings.Contains(out, "hunter2") {
		t.Fatalf("set pass: %v %q", err, out)
	}
	if _, _, err := cli(t, tool, "config", "set", "ssh", "nas2"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"config", "show"}, {"config", "show", "--json", "{}"}} {
		out, _, err = cli(t, tool, args...)
		if err != nil || strings.Contains(out, "hunter2") || !strings.Contains(out, "nas2") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if !strings.Contains(out, `"pass_set": true`) || !strings.Contains(out, `"O_QBT_URL"`) {
		t.Fatalf("show: %s", out)
	}
	c, _, _, _ := tool.config()
	if c.Pass != "hunter2" || c.SSH != "nas2" || c.Volume != "/volume1" {
		t.Fatalf("config %+v", c)
	}
	out, _, _ = cli(t, tool, "config", "init")
	if !strings.Contains(out, "exists; left unchanged") {
		t.Fatalf("second init: %s", out)
	}
}

func TestCatalogGolden(t *testing.T) {
	tool := testTool(t, "http://127.0.0.1:1", "", nil)
	out, _, err := cli(t, tool, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "catalog.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if out != string(want) {
		t.Fatalf("catalog differs from %s (go test -run Catalog -update)", golden)
	}
	var doc ocli.CatalogDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, n := range doc.Nouns {
		for _, v := range n.Verbs {
			if v.Name == "list" {
				rows[n.Name] = v.Rows
			}
		}
	}
	for _, noun := range listNouns {
		if rows[noun] != noun {
			t.Errorf("%s list declares rows %q", noun, rows[noun])
		}
	}
	if doc.State != "app.state" {
		t.Errorf("state %q", doc.State)
	}
}

func TestUnknownWordIsNotFound(t *testing.T) {
	tool := testTool(t, "http://127.0.0.1:1", "", nil)
	out, errOut, err := cli(t, tool, "torrentz")
	if oden.ExitCode(err) != 1 || out != "" || !strings.HasPrefix(errOut, "o-qbt: command \"torrentz\" not found\ndid you mean: torrent\n") {
		t.Fatalf("torrentz: %v %q\n%s", err, out, errOut)
	}
}
