package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// The command line is an ochazuke runtime behind ochazuke/cli. Lists are
// torrent (row id = the hash, what --target takes) and category (row id
// category/NAME); app state answers both as JSON Lines (the catalog's
// state). A --target no list shows yet is revealed by loading the torrent
// list, so a confirm prompt reads the torrent's label.

// Version is the catalog's version.
const Version = "0.1.0"

// nouns are this tool's nouns, mounted as commands.
var nouns = []string{"app", "torrent", "tracker", "transfer", "guard", "category", "config"}

// listNouns are the nouns with rows.
var listNouns = []string{"torrent", "category"}

func params(kv ...any) map[string]ochazuke.Parameter {
	out := map[string]ochazuke.Parameter{}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1].(ochazuke.Parameter)
	}
	return out
}

var (
	allParam   = ochazuke.Parameter{Type: "boolean", Description: "every torrent instead of --target HASH"}
	limitParam = ochazuke.Parameter{Type: "string", Description: "allowance: how far free space may drop below the baseline: bytes, or a number with K, M, G or T (default 10G)"}
)

// answer is a verb's Result; a Texter's rendering is kept for a person.
func (t *Tool) answer(v any, err error) ochazuke.Result {
	if err != nil {
		return ochazuke.Failed(err)
	}
	if x, ok := v.(Texter); ok && t.human {
		s := x.Text()
		t.text = &s
	}
	return ochazuke.Done(v)
}

func (t *Tool) shell() *ocli.Shell {
	r := ochazuke.NewRuntime()
	lists := map[string]*ochazuke.List{}
	var err error
	for _, noun := range listNouns {
		var list *ochazuke.List
		list, err = r.AddList("list/"+noun, nil)
		if err != nil {
			panic(err) // a fresh runtime takes every list
		}
		lists[noun] = list
	}
	// tracker rows are one torrent's (tracker list); app state skips them,
	// since all of them cost a request per torrent.
	if lists["tracker"], err = r.AddList("list/tracker", nil); err != nil {
		panic(err)
	}
	ctxOf := func(c ochazuke.Ctx) context.Context { return context.Background() }
	// api is the effective config's session.
	api := func() (*Client, error) {
		c, _, _, err := t.config()
		if err != nil {
			return nil, err
		}
		return t.client(c), nil
	}
	setRows := func(noun string, rows []Row) error {
		items := make([]ochazuke.ListItem, len(rows))
		for i, row := range rows {
			items[i] = ochazuke.ListItem{ID: row.ID, Label: row.Label, Noun: noun, Data: row.ID}
		}
		return lists[noun].SetItems(items)
	}
	// torrentsOf is the torrents q selects, set as the torrent list's rows.
	torrentsOf := func(ctx context.Context, q TorrentQuery) ([]Torrent, []Row, error) {
		c, err := api()
		if err != nil {
			return nil, nil, err
		}
		ts, err := Torrents(ctx, c, q)
		if err != nil {
			return nil, nil, err
		}
		rows := make([]Row, len(ts))
		for i, x := range ts {
			rows[i] = torrentRow(x)
		}
		return ts, rows, setRows("torrent", rows)
	}
	torrentRows := func(ctx context.Context) ([]Row, error) {
		_, rows, err := torrentsOf(ctx, TorrentQuery{})
		return rows, err
	}
	categoriesOf := func(ctx context.Context) ([]Category, []Row, error) {
		c, err := api()
		if err != nil {
			return nil, nil, err
		}
		cs, err := Categories(ctx, c)
		if err != nil {
			return nil, nil, err
		}
		rows := make([]Row, len(cs))
		for i, x := range cs {
			rows[i] = Row{ID: "category/" + x.Name, Label: x.Name + "  " + x.SavePath, Data: x}
		}
		return cs, rows, setRows("category", rows)
	}
	categoryRows := func(ctx context.Context) ([]Row, error) {
		_, rows, err := categoriesOf(ctx)
		return rows, err
	}
	loc := t.now().Location()
	// hashes is what a torrent verb acts on: --target's hash, or all.
	hashes := func(c ochazuke.Ctx, verb string) (string, error) {
		all, _ := c.Args["all"].(bool)
		var hash string
		if c.Instance != nil && c.Instance.Noun == "torrent" {
			hash, _ = c.Instance.Data.(string)
		}
		switch {
		case all && hash != "":
			return "", fmt.Errorf("torrent %s: --target or --all, not both", verb)
		case all:
			return "all", nil
		case hash == "":
			return "", fmt.Errorf("torrent %s needs --target HASH (o qbt torrent list) or --all", verb)
		}
		return hash, nil
	}

	r.Define("app").
		Verb("info", func(c ochazuke.Ctx) ochazuke.Result {
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			i, err := AppInfo(ctxOf(c), cl)
			return t.present(c, i, InfoTable(i), err)
		}, "the credentials test: log in once and read the version, Web API version, build, speeds and free disk; changes nothing").
		Verb("prefs", func(c ochazuke.Ctx) ochazuke.Result {
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			return t.answer(Prefs(ctxOf(c), cl))
		}, "selected preferences: save and temp paths, max active downloads/uploads/torrents, dl/up limits (bytes/s)").
		Verb("state", func(c ochazuke.Ctx) ochazuke.Result {
			var out ocli.Lines
			for _, noun := range listNouns {
				load := map[string]func(context.Context) ([]Row, error){"torrent": torrentRows, "category": categoryRows}[noun]
				rows, err := load(ctxOf(c))
				if err != nil {
					return ochazuke.Failed(err)
				}
				for _, row := range rows {
					out = append(out, ocli.Row{Noun: noun, ID: row.ID, Label: row.Label})
				}
			}
			return ochazuke.Done(out)
		}, "every list's rows in one call, JSON Lines {noun, id, label} for torrent and category (the catalog's state)").State()

	// listTorrents answers a torrent list-like verb: the rows (a host's
	// --json), else the torrents in --format; preset fixes the filter.
	listTorrents := func(c ochazuke.Ctx, preset string, need func(ochazuke.Ctx) error) ochazuke.Result {
		if need != nil {
			if err := need(c); err != nil {
				return ochazuke.Failed(err)
			}
		}
		q, err := torrentQuery(c, preset)
		if err != nil {
			return ochazuke.Failed(err)
		}
		ts, rows, err := torrentsOf(ctxOf(c), q)
		return t.present(c, rows, TorrentTable(ts, loc), err)
	}
	r.Define("torrent").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result { return listTorrents(c, "", nil) },
			"the torrent rows: {id: hash, label: \"state  progress%  name\", data: {state, progress, size, dlspeed, eta, category, added_on, completed_on, save_path}}; --filter, --category, --tag, --sort, --reverse, --limit are torrents/info's query; --format, --columns, --out render them (a host's --json without --format gets the rows)").Rows("torrent").
		Verb("export", func(c ochazuke.Ctx) ochazuke.Result {
			return listTorrents(c, "", func(c ochazuke.Ctx) error {
				if ochazuke.StringArg(c.Args, "out", "") == "" {
					return errors.New("torrent export needs --out FILE (the format follows its extension, else --format, else json)")
				}
				return nil
			})
		}, "write the torrents to --out FILE in --format (default from the extension, else json); takes torrent list's query and --columns").
		Verb("summary", func(c ochazuke.Ctx) ochazuke.Result {
			q, err := torrentQuery(c, "")
			if err != nil {
				return ochazuke.Failed(err)
			}
			ts, _, err := torrentsOf(ctxOf(c), q)
			ss := Summarize(ts)
			return t.present(c, ss, SummaryTable(ss), err)
		}, "count and total size per state, the total last, one table: [{state, count, size}]; takes --filter, --category, --tag").
		Verb("overview", func(c ochazuke.Ctx) ochazuke.Result {
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			ts, _, err := torrentsOf(ctxOf(c), TorrentQuery{})
			if err != nil {
				return ochazuke.Failed(err)
			}
			st, err := MainState(ctxOf(c), cl)
			if err != nil {
				return ochazuke.Failed(err)
			}
			g, err := t.guardState(st.Free)
			if err != nil {
				return ochazuke.Failed(err)
			}
			o := Overviews(ts, st, g)
			return t.present(c, o, OverviewView{O: o, Loc: loc}, nil)
		}, "one screen: totals (torrents, active, seeding, downloading, paused, errored, size, speeds, free space, the guard), counts and size per state, top 5 by download and upload speed, the 5 oldest unfinished, categories; json is {totals, states, top_download, top_upload, oldest_unfinished, categories}").
		Verb("show", func(c ochazuke.Ctx) ochazuke.Result {
			hash, err := hashes(c, "show")
			if err == nil && hash == "all" {
				err = errors.New("torrent show takes --target HASH, not --all")
			}
			if err != nil {
				return ochazuke.Failed(err)
			}
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			return t.answer(TorrentShow(ctxOf(c), cl, hash))
		}, "one torrent's info and properties: --target HASH")
	for _, v := range []struct{ verb, desc string }{
		{"pause", "stop --target HASH or --all (torrents/stop, else v4's torrents/pause)"},
		{"resume", "start --target HASH or --all (torrents/start, else v4's torrents/resume)"},
		{"recheck", "recheck --target HASH or --all"},
	} {
		r.Define("torrent").Verb(v.verb, func(c ochazuke.Ctx) ochazuke.Result {
			h, err := hashes(c, v.verb)
			if err != nil {
				return ochazuke.Failed(err)
			}
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			ctx := ctxOf(c)
			var path string
			switch v.verb {
			case "pause":
				path, err = cl.Pause(ctx, h)
			case "resume":
				path, err = cl.Resume(ctx, h)
			default:
				path = "/api/v2/torrents/recheck"
				err = cl.Post(ctx, path, url.Values{"hashes": {h}})
			}
			return t.answer(Message{Message: fmt.Sprintf("%s %s (%s)", v.verb, h, path)}, err)
		}, v.desc)
		r.Operations["torrent."+v.verb] = withParams(r.Operations["torrent."+v.verb], params("all", allParam))
	}
	r.Define("torrent").Verb("delete", func(c ochazuke.Ctx) ochazuke.Result {
		files, _ := c.Args["files"].(bool)
		what := "kept their files"
		if files {
			what = "deleted their files"
		}
		var hs []string
		if bulk := deleteSelects(c); bulk {
			if c.Instance != nil && c.Instance.Noun == "torrent" && c.Instance.Data != nil {
				return ochazuke.Failed(errors.New("torrent delete: --target or a selection (--hashes, --state, --filter, --category, --tag), not both"))
			}
			if v := ochazuke.StringArg(c.Args, "hashes", ""); v != "" {
				hs = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '|' })
			} else {
				q, err := torrentQuery(c, "")
				if err != nil {
					return ochazuke.Failed(err)
				}
				cl, err := api()
				if err != nil {
					return ochazuke.Failed(err)
				}
				ts, err := Torrents(ctxOf(c), cl, q)
				if err != nil {
					return ochazuke.Failed(err)
				}
				state := ochazuke.StringArg(c.Args, "state", "")
				for _, x := range ts {
					if state == "" || x.State == state {
						hs = append(hs, x.Hash)
					}
				}
			}
		} else {
			h, err := hashes(c, "delete")
			if err == nil && h == "all" {
				err = errors.New("torrent delete takes --target HASH or a selection, never --all")
			}
			if err != nil {
				return ochazuke.Failed(err)
			}
			hs = []string{h}
		}
		cl, err := api()
		if err != nil {
			return ochazuke.Failed(err)
		}
		done := 0
		for chunk := range slices.Chunk(hs, deleteBatch) {
			if err := cl.Post(ctxOf(c), "/api/v2/torrents/delete", url.Values{"hashes": {strings.Join(chunk, "|")}, "deleteFiles": {strconv.FormatBool(files)}}); err != nil {
				return ochazuke.Failed(fmt.Errorf("torrent delete: %d of %d removed: %w", done, len(hs), err))
			}
			done += len(chunk)
			if len(hs) > deleteBatch && t.Stderr != nil {
				fmt.Fprintf(t.Stderr, "deleted %d/%d\n", done, len(hs))
			}
		}
		if len(hs) == 1 {
			return t.answer(Message{Message: "deleted torrent " + hs[0] + ", " + strings.Replace(what, "their", "its", 1)}, nil)
		}
		return t.answer(Message{Message: fmt.Sprintf("deleted %d torrents, %s", len(hs), what)}, nil)
	}, "remove --target HASH, or a selection (--hashes H1,H2; --state STATE, the exact qBittorrent state; --filter, --category, --tag), from qBittorrent in batches of 200; --files deletes their data too (asks first; --yes skips the question)").Confirm("Delete torrent {{label}}?")
	r.Operations["torrent.delete"] = withParams(r.Operations["torrent.delete"], params(append([]any{
		"files", ochazuke.Parameter{Type: "boolean", Description: "delete the downloaded data as well"},
		"hashes", ochazuke.Parameter{Type: "string", Description: "comma-separated hashes to remove instead of --target"},
		"state", ochazuke.Parameter{Type: "string", Description: "only torrents in exactly this state (missingFiles, error, ...)"},
	}, queryParams(true)[:6]...)...))
	for _, p := range torrentPresets {
		r.Define("torrent").Verb(p.Verb, func(c ochazuke.Ctx) ochazuke.Result { return listTorrents(c, p.Filter, nil) },
			"alias of torrent list --filter "+p.Filter+": "+p.About)
		r.Operations["torrent."+p.Verb] = withParams(r.Operations["torrent."+p.Verb], params(append(queryParams(false), outputParams()...)...))
	}
	for _, verb := range []string{"list", "export"} {
		r.Operations["torrent."+verb] = withParams(r.Operations["torrent."+verb], params(append(queryParams(true), outputParams()...)...))
	}
	r.Operations["torrent.summary"] = withParams(r.Operations["torrent.summary"], params(append(queryParams(true)[:6], outputParams()...)...))
	r.Operations["torrent.overview"] = withParams(r.Operations["torrent.overview"], params(outputParams()[:4]...))

	// trackersOf is every tracker of the torrents c's query selects, one
	// request per torrent through the pool, with a progress line on stderr.
	trackersOf := func(c ochazuke.Ctx) ([][]Tracker, error) {
		q, err := torrentQuery(c, "")
		if err != nil {
			return nil, err
		}
		cl, err := api()
		if err != nil {
			return nil, err
		}
		ts, err := Torrents(ctxOf(c), cl, q)
		if err != nil {
			return nil, err
		}
		trs, err := TrackersOf(ctxOf(c), cl, ts, func(done, total int) {
			fmt.Fprintf(t.Stderr, "\rtrackers: %d/%d torrents", done, total)
		})
		if len(ts) > 0 {
			fmt.Fprintln(t.Stderr)
		}
		return trs, err
	}
	r.Define("tracker").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result {
			var hash string
			if c.Instance != nil && c.Instance.Noun == "torrent" {
				hash, _ = c.Instance.Data.(string)
			}
			if hash == "" {
				return ochazuke.Failed(errors.New("tracker list needs --target HASH (o qbt torrent list)"))
			}
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			trs, err := Trackers(ctxOf(c), cl, hash)
			if err != nil {
				return ochazuke.Failed(err)
			}
			full, _ := c.Args["full-url"].(bool)
			rows := trackerRows(trs, full)
			if err := setRows("tracker", rows); err != nil {
				return ochazuke.Failed(err)
			}
			return t.present(c, rows, TrackerTable(rows, full), nil)
		}, "one torrent's trackers, without DHT/PeX/LSD: --target HASH; rows {id: host, label: \"host  status\", data: {url, status, tier, peers, seeds, leeches, msg}}; url is the scheme and host only (passkeys live in the path) unless --full-url; columns host status tier peers seeds leeches msg (default), url").Rows("tracker").
		Verb("summary", func(c ochazuke.Ctx) ochazuke.Result {
			trs, err := trackersOf(c)
			hs := SummarizeTrackers(trs)
			return t.present(c, hs, TrackerSummaryTable(hs), err)
		}, "one row per tracker host across the torrents --filter, --category, --tag select: [{host, torrents, working, not_working, msg}]; working counts torrents with an entry there in status 2, not_working those with one in status 4 and none working; msg is the host's most common message (60 chars); one request per torrent, 8 at a time, progress on stderr").
		Verb("hosts", func(c ochazuke.Ctx) ochazuke.Result {
			trs, err := trackersOf(c)
			if err != nil {
				return ochazuke.Failed(err)
			}
			hosts := TrackerHosts(trs)
			if t.human && ochazuke.StringArg(c.Args, "format", "") == "" && ochazuke.StringArg(c.Args, "out", "") == "" {
				s := ""
				if len(hosts) > 0 {
					s = strings.Join(hosts, "\n") + "\n"
				}
				t.text = &s
				return ochazuke.Done(hosts)
			}
			return t.present(c, hosts, HostsTable(hosts), nil)
		}, "the distinct tracker hosts (scheme://host) of the torrents --filter, --category, --tag select, one per line for piping (--format renders a host table)")
	r.Operations["tracker.list"] = withParams(r.Operations["tracker.list"], params(append([]any{
		"full-url", ochazuke.Parameter{Type: "boolean", Description: "data.url and the url column carry the whole announce URL, passkey included (for the owner only)"}}, outputParams()...)...))
	for _, verb := range []string{"summary", "hosts"} {
		r.Operations["tracker."+verb] = withParams(r.Operations["tracker."+verb], params(append(queryParams(true)[:6], outputParams()...)...))
	}

	r.Define("transfer").
		Verb("info", func(c ochazuke.Ctx) ochazuke.Result {
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			x, err := TransferInfo(ctxOf(c), cl)
			return t.present(c, x, TransferTable(x), err)
		}, "global speeds, session totals, limits, connection status, alternative limits on or off").
		Verb("limit", func(c ochazuke.Ctx) ochazuke.Result {
			_, hasDL := c.Args["dl"]
			_, hasUP := c.Args["up"]
			if !hasDL && !hasUP {
				return ochazuke.Failed(errors.New("transfer limit needs --dl N and/or --up N (bytes/s; 0 = unlimited)"))
			}
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			ctx := ctxOf(c)
			for _, x := range []struct {
				set  bool
				name string
				path string
			}{{hasDL, "dl", "/api/v2/transfer/setDownloadLimit"}, {hasUP, "up", "/api/v2/transfer/setUploadLimit"}} {
				if !x.set {
					continue
				}
				n := ochazuke.IntArg(c.Args, x.name, 0)
				if n < 0 {
					return ochazuke.Failed(fmt.Errorf("--%s %d: a limit is 0 (unlimited) or more", x.name, n))
				}
				if err := cl.Post(ctx, x.path, url.Values{"limit": {strconv.Itoa(n)}}); err != nil {
					return ochazuke.Failed(err)
				}
			}
			return t.answer(TransferInfo(ctx, cl))
		}, "set the global download and/or upload limit in bytes/s (0 = unlimited); prints transfer info").
		Verb("speed-limits-toggle", func(c ochazuke.Ctx) ochazuke.Result {
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			ctx := ctxOf(c)
			if err := cl.Post(ctx, "/api/v2/transfer/toggleSpeedLimitsMode", nil); err != nil {
				return ochazuke.Failed(err)
			}
			return t.answer(TransferInfo(ctx, cl))
		}, "switch the alternative speed limits on or off; prints transfer info")
	r.Operations["transfer.limit"] = withParams(r.Operations["transfer.limit"], params(
		"dl", ochazuke.Parameter{Type: "integer", Description: "download limit, bytes/s (0 = unlimited)"},
		"up", ochazuke.Parameter{Type: "integer", Description: "upload limit, bytes/s (0 = unlimited)"}))

	guardVerb := func(verb, desc string, run func(ctx context.Context, c ochazuke.Ctx, gs GuardSettings) (any, error)) {
		r.Define("guard").Verb(verb, func(c ochazuke.Ctx) ochazuke.Result {
			conf, _, _, err := t.config()
			if err != nil {
				return ochazuke.Failed(err)
			}
			gs, err := guardSettings(conf, c.Args)
			if err != nil {
				return ochazuke.Failed(err)
			}
			v, err := run(ctxOf(c), c, gs)
			return t.answer(v, err)
		}, desc)
	}
	guardVerb("check", "the monitor step: df the volume over ssh; floor mode (default) pauses every torrent when free space is below --min-free (5GiB) and resumes them above --resume-above (20GiB) when the guard paused; allowance mode pauses once free space is more than --limit below the baseline; one line to guard.log", func(ctx context.Context, _ ochazuke.Ctx, gs GuardSettings) (any, error) {
		return t.GuardCheck(ctx, gs)
	})
	guardVerb("status", "the launchd agent, the last guard.log line, the mode and its thresholds, free space (against the baseline in allowance mode), whether the guard paused", func(ctx context.Context, _ ochazuke.Ctx, gs GuardSettings) (any, error) {
		return t.GuardStatusOf(ctx, gs)
	})
	guardVerb("rebase", "store the current free space as the baseline (or --bytes N) and clear the marker; only allowance mode reads the baseline", func(ctx context.Context, c ochazuke.Ctx, gs GuardSettings) (any, error) {
		n := ochazuke.IntArg(c.Args, "bytes", 0)
		if n < 0 {
			return nil, errors.New("--bytes is a positive byte count")
		}
		return t.GuardRebase(ctx, int64(n), gs)
	})
	guardVerb("resume", "resume every torrent and clear the marker", func(ctx context.Context, _ ochazuke.Ctx, gs GuardSettings) (any, error) {
		return t.GuardResume(ctx, gs)
	})
	r.Define("guard").
		Verb("install", func(c ochazuke.Ctx) ochazuke.Result { return t.answer(t.GuardInstall(ctxOf(c))) },
			"write ~/Library/LaunchAgents/"+GuardLabel+".plist (o-qbt guard check every 300 s and at load), lint it, bootstrap it in gui/UID; idempotent").
		Verb("uninstall", func(c ochazuke.Ctx) ochazuke.Result { return t.answer(t.GuardUninstall(ctxOf(c))) },
			"bootout the agent and remove its plist; the baseline, marker and log stay").
		Verb("log", func(c ochazuke.Ctx) ochazuke.Result {
			return t.answer(t.GuardLogTail(ochazuke.IntArg(c.Args, "tail", 20)), nil)
		}, "the last --tail N lines of guard.log (default 20)").Positional("tail")
	guardParams := []any{"limit", limitParam,
		"mode", ochazuke.Parameter{Type: "string", Enum: GuardModes, Description: "the rule: floor (pause below --min-free, resume above --resume-above) or allowance (pause --limit below the baseline); default config guard.mode, else floor"},
		"min-free", ochazuke.Parameter{Type: "string", Description: "floor: pause when free space is below this size (default config guard.minFree, else 5GiB)"},
		"resume-above", ochazuke.Parameter{Type: "string", Description: "floor: resume, when the guard paused, once free space is above this size (default config guard.resumeAbove, else 20GiB)"}}
	for _, verb := range []string{"check", "status", "resume"} {
		r.Operations["guard."+verb] = withParams(r.Operations["guard."+verb], params(guardParams...))
	}
	r.Operations["guard.rebase"] = withParams(r.Operations["guard.rebase"], params(append(guardParams,
		"bytes", ochazuke.Parameter{Type: "integer", Description: "the baseline in bytes instead of the current free space"})...))
	r.Operations["guard.log"] = withParams(r.Operations["guard.log"], params("tail", ochazuke.Parameter{Type: "integer", Description: "how many lines (default 20)"}))

	r.Define("category").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result {
			cs, rows, err := categoriesOf(ctxOf(c))
			return t.present(c, rows, CategoryTable(cs), err)
		}, "the category rows: {id: category/NAME, label: \"NAME  SAVE_PATH\", data: {name, savePath}}; --format, --columns (name, save_path), --out").Rows("category")

	r.Define("config").
		Verb("init", func(ochazuke.Ctx) ochazuke.Result { return t.answer(t.ConfigInit()) },
			"write the settings file (url, user admin, empty pass, volume, ssh), mode 600, unless it exists; prints its path").
		Verb("show", func(ochazuke.Ctx) ochazuke.Result { return t.answer(t.ConfigShow()) },
			"the effective settings and the environment overrides; never the password, only whether one is set").
		Verb("set", func(c ochazuke.Ctx) ochazuke.Result {
			stdin, _ := c.Args["stdin"].(bool)
			return t.answer(t.ConfigSet(ochazuke.StringArg(c.Args, "key", ""), ochazuke.StringArg(c.Args, "value", ""), stdin))
		}, "set KEY to VALUE; pass is never taken from the command line: it prompts with echo off, or reads one line with --stdin").Positional("key", "value")
	r.Operations["config.set"] = withParams(r.Operations["config.set"], params(
		"key", ochazuke.Parameter{Type: "string", Required: true, Enum: configKeys, Description: "the setting"},
		"value", ochazuke.Parameter{Type: "string", Description: "the new value (not for pass)"},
		"stdin", ochazuke.Parameter{Type: "boolean", Description: "read the value from one line of stdin"}))

	for _, key := range []string{"app.info", "transfer.info", "category.list"} {
		r.Operations[key] = withParams(r.Operations[key], params(outputParams()...))
	}

	sh := ocli.New(r, "o-qbt", t.Stdin, t.Stdout, t.Stderr)
	sh.Usage = ocli.DefaultUsage(t.Prog)
	sh.Version = Version
	// Reveal loads the torrent list so a --target hash is a row with its
	// label (a confirm prompt reads it); an unknown hash is an error.
	sh.Reveal = func(target string) error {
		rows, err := torrentRows(context.Background())
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ID == target {
				return nil
			}
		}
		return fmt.Errorf("no torrent with hash %s (o qbt torrent list)", target)
	}
	return sh
}

func withParams(op ochazuke.Operation, p map[string]ochazuke.Parameter) ochazuke.Operation {
	op.Parameters = p
	return op
}

// torrentPresets are torrent verbs that are torrent list with a fixed
// --filter: data, not code paths.
var torrentPresets = []struct{ Verb, Filter, About string }{
	{"active", "active", "torrents moving data now"},
	{"seeding", "seeding", "complete torrents uploading or waiting to"},
	{"downloading", "downloading", "incomplete torrents downloading or waiting to"},
	{"paused", "paused", "stopped torrents (v5 calls the filter stopped)"},
	{"errored", "errored", "torrents in error or missing files"},
	{"stalled", "stalled", "torrents with peers but no data moving"},
}

// queryParams are torrents/info's query as parameters (filter first, when
// the verb takes it), name/value pairs for params.
func queryParams(filter bool) []any {
	kv := []any{
		"filter", ochazuke.Parameter{Type: "string", Enum: TorrentFilters, Description: "torrents/info's filter (default all)"},
		"category", ochazuke.Parameter{Type: "string", Description: "only this category (\"\" = uncategorised)"},
		"tag", ochazuke.Parameter{Type: "string", Description: "only this tag (\"\" = untagged)"},
		"sort", ochazuke.Parameter{Type: "string", Enum: sortFields(), Description: "sort by this field on the server (default name, here)"},
		"reverse", ochazuke.Parameter{Type: "boolean", Description: "reverse the order"},
		"limit", ochazuke.Parameter{Type: "integer", Description: "at most N torrents"},
	}
	if !filter {
		return kv[2:]
	}
	return kv
}

func sortFields() []string {
	out := make([]string, 0, len(TorrentSorts))
	for k := range TorrentSorts {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// outputParams are the output engine's parameters: format, out, columns.
func outputParams() []any {
	return []any{
		"format", ochazuke.Parameter{Type: "string", Enum: Formats, Description: "table (the default on a terminal), json (otherwise), jsonl, tsv, csv, md or toon"},
		"out", ochazuke.Parameter{Type: "string", Description: "write to FILE instead of printing; its extension picks the format when --format is absent"},
		"columns", ochazuke.Parameter{Type: "string", Description: "comma-separated columns, in order, for table, tsv, csv, md and toon"},
	}
}

// torrentQuery is c's torrents/info query; preset, when set, is the filter.
func torrentQuery(c ochazuke.Ctx, preset string) (TorrentQuery, error) {
	q := TorrentQuery{Filter: ochazuke.StringArg(c.Args, "filter", ""), Sort: ochazuke.StringArg(c.Args, "sort", "")}
	if preset != "" {
		q.Filter = preset
	}
	for name, dst := range map[string]**string{"category": &q.Category, "tag": &q.Tag} {
		if v, ok := c.Args[name].(string); ok {
			*dst = &v
		}
	}
	q.Reverse, _ = c.Args["reverse"].(bool)
	if q.Limit = ochazuke.IntArg(c.Args, "limit", 0); q.Limit < 0 {
		return q, fmt.Errorf("--limit %d: a count of 1 or more", q.Limit)
	}
	return q, nil
}

// View is an answer the output engine renders.
type View interface {
	Render(format string, columns []string) (string, error)
}

// present answers a list-like verb. A host's --json without --format gets
// native as JSON (rows, for a list). Otherwise v is rendered in --format,
// by default table on a terminal and json elsewhere (with --out, the
// file's extension, else json), and printed or written to --out.
func (t *Tool) present(c ochazuke.Ctx, native any, v View, err error) ochazuke.Result {
	if err != nil {
		return ochazuke.Failed(err)
	}
	format, out := ochazuke.StringArg(c.Args, "format", ""), ochazuke.StringArg(c.Args, "out", "")
	if format == "" && out != "" {
		if format = formatOf(out); format == "" {
			format = "json"
		}
	}
	if format == "" {
		if !t.human {
			return ochazuke.Done(native)
		}
		format = "json"
		if t.TTY {
			format = "table"
		}
	}
	var columns []string
	for _, name := range strings.Split(ochazuke.StringArg(c.Args, "columns", ""), ",") {
		if name = strings.TrimSpace(name); name != "" {
			columns = append(columns, name)
		}
	}
	s, err := v.Render(format, columns)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if out != "" {
		if err := os.WriteFile(out, []byte(s), 0o644); err != nil {
			return ochazuke.Failed(err)
		}
		what := ""
		if tb, ok := v.(Table); ok {
			what = fmt.Sprintf(" %d %s rows", tb.Len(), tb.Name)
		}
		m := Message{Message: fmt.Sprintf("wrote%s to %s (%s)", what, out, format), Path: out}
		if !t.human {
			return ochazuke.Done(m)
		}
		text := m.Text()
		t.text = &text
		return ochazuke.Done(m)
	}
	t.text = &s
	return ochazuke.Done(native)
}

// guardState is the guard as the overview shows it, against free bytes.
func (t *Tool) guardState(free int64) (GuardState, error) {
	c, _, _, err := t.config()
	if err != nil {
		return GuardState{}, err
	}
	gs, err := guardSettings(c, nil)
	if err != nil {
		return GuardState{}, err
	}
	g := GuardState{Mode: gs.Mode, MinFree: gs.MinFree, ResumeAbove: gs.ResumeAbove, Paused: t.marker(), Limit: gs.Limit}
	base, ok, err := t.readBaseline()
	if ok {
		g.Baseline, g.Used = base, base-free
	}
	return g, err
}

// deleteBatch is how many hashes one torrents/delete request carries.
var deleteBatch = 200

// deleteSelects reports whether c selects torrents for delete by
// --hashes or a query rather than --target.
func deleteSelects(c ochazuke.Ctx) bool {
	for _, k := range []string{"hashes", "state", "filter", "category", "tag"} {
		if _, ok := c.Args[k]; ok {
			return true
		}
	}
	return false
}
