package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeQBT is a qBittorrent Web API v2 double: one user, a SID cookie, a
// few torrents, and a record of every request.
type fakeQBT struct {
	*httptest.Server
	mu      sync.Mutex
	pass    string
	ban     bool // login answers 403
	v4      bool // no torrents/stop or start: 404, as qBittorrent 4 answers
	logins  int
	calls   []string // "METHOD /path hashes=..." per request
	removed []string
}

func newFake(t *testing.T) *fakeQBT {
	f := &fakeQBT{pass: "good"}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeQBT) serve(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+r.Form.Get("hashes")))
	if r.URL.Path == "/api/v2/auth/login" {
		f.logins++
		switch {
		case f.ban:
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "Your IP address has been banned after too many failed authentication attempts.")
		case r.Form.Get("username") == "admin" && r.Form.Get("password") == f.pass:
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s1", Path: "/"})
			fmt.Fprint(w, "Ok.")
		default:
			fmt.Fprint(w, "Fails.")
		}
		return
	}
	if c, err := r.Cookie("SID"); err != nil || c.Value != "s1" {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "Forbidden")
		return
	}
	switch r.URL.Path {
	case "/api/v2/app/version":
		fmt.Fprint(w, "v5.0.4")
	case "/api/v2/app/webapiVersion":
		fmt.Fprint(w, "2.11.2")
	case "/api/v2/app/buildInfo":
		fmt.Fprint(w, `{"qt":"6.7.2","libtorrent":"2.0.10.0","boost":"1.86.0","openssl":"3.3.2","bitness":64}`)
	case "/api/v2/app/preferences":
		fmt.Fprint(w, `{"save_path":"/downloads/","temp_path":"/incomplete/","temp_path_enabled":true,"max_active_downloads":3,"dl_limit":0,"up_limit":1048576,"web_ui_password":"x"}`)
	case "/api/v2/transfer/info":
		fmt.Fprint(w, `{"dl_info_speed":2097152,"up_info_speed":1024,"dl_info_data":5,"up_info_data":6,"dl_rate_limit":0,"up_rate_limit":0,"dht_nodes":300,"connection_status":"connected"}`)
	case "/api/v2/transfer/speedLimitsMode":
		fmt.Fprint(w, "0")
	case "/api/v2/sync/maindata":
		fmt.Fprint(w, `{"rid":1,"full_update":true,"server_state":{"free_space_on_disk":628000000000}}`)
	case "/api/v2/torrents/info":
		fmt.Fprint(w, `[{"hash":"bbb","name":"Zeta","state":"stalledUP","progress":1,"size":2048,"dlspeed":0,"eta":8640000,"category":"tv","added_on":10,"completion_on":20,"save_path":"/downloads/tv"},
{"hash":"aaa","name":"alpha","state":"downloading","progress":0.4217,"size":4096,"dlspeed":512,"eta":60,"category":"","added_on":30,"completion_on":-1,"save_path":"/downloads"}]`)
	case "/api/v2/torrents/properties":
		fmt.Fprint(w, `{"save_path":"/downloads","comment":"c"}`)
	case "/api/v2/torrents/categories":
		fmt.Fprint(w, `{"tv":{"name":"tv","savePath":"/downloads/tv"},"movies":{"name":"movies","savePath":""}}`)
	case "/api/v2/torrents/stop", "/api/v2/torrents/start":
		if f.v4 {
			w.WriteHeader(http.StatusNotFound)
		}
	case "/api/v2/torrents/delete":
		f.removed = append(f.removed, r.Form.Get("hashes")+" files="+r.Form.Get("deleteFiles"))
	case "/api/v2/torrents/pause", "/api/v2/torrents/resume", "/api/v2/torrents/recheck",
		"/api/v2/transfer/setDownloadLimit", "/api/v2/transfer/setUploadLimit", "/api/v2/transfer/toggleSpeedLimitsMode":
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// posted is the non-login POSTs, in order.
func (f *fakeQBT) posted() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "POST ") && !strings.Contains(c, "/auth/login") {
			out = append(out, strings.TrimPrefix(c, "POST /api/v2/"))
		}
	}
	return out
}

func (f *fakeQBT) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// testTool is a Tool in a temporary home whose config points at url with
// pass (none when ""), a fixed clock, a df answering *free, and launchctl
// reporting nothing loaded. It never reaches a real server or the NAS.
func testTool(t *testing.T, url, pass string, free *int64) *Tool {
	home := t.TempDir()
	env := []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, "cfg"), "XDG_DATA_HOME=" + filepath.Join(home, "data"), "O_QBT_URL=" + url}
	if pass != "" {
		env = append(env, "O_QBT_PASS="+pass)
	}
	clock := time.Date(2026, 10, 25, 12, 30, 0, 0, time.FixedZone("EDT", -4*3600))
	return &Tool{
		Stdin: strings.NewReader(""), Env: env, Home: home, UID: 501, Prog: "o-qbt",
		now: func() time.Time { return clock },
		df: func(context.Context, Config) (int64, error) {
			if free == nil {
				return 0, errors.New("no df in this test")
			}
			return *free, nil
		},
		run: func(_ context.Context, name string, args ...string) (string, error) {
			return "", fmt.Errorf("%s: not run in tests", name)
		},
	}
}

// cli runs one command on a fresh process state (a new Client, so a new
// login) and returns stdout and stderr.
func cli(t *testing.T, tool *Tool, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	tool.Stdout, tool.Stderr, tool.cl = &out, &errOut, nil
	err := tool.Main(context.Background(), args)
	return out.String(), errOut.String(), err
}
