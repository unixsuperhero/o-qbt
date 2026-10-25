package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"

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
var nouns = []string{"app", "torrent", "transfer", "guard", "category", "config"}

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
	limitParam = ochazuke.Parameter{Type: "string", Description: "how far free space may drop below the baseline: bytes, or a number with K, M, G or T (default 10G)"}
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
	for _, noun := range listNouns {
		list, err := r.AddList("list/"+noun, nil)
		if err != nil {
			panic(err) // a fresh runtime takes every list
		}
		lists[noun] = list
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
	torrentRows := func(ctx context.Context) ([]Row, error) {
		c, err := api()
		if err != nil {
			return nil, err
		}
		ts, err := Torrents(ctx, c)
		if err != nil {
			return nil, err
		}
		rows := make([]Row, len(ts))
		for i, x := range ts {
			rows[i] = torrentRow(x)
		}
		return rows, setRows("torrent", rows)
	}
	categoryRows := func(ctx context.Context) ([]Row, error) {
		c, err := api()
		if err != nil {
			return nil, err
		}
		cs, err := Categories(ctx, c)
		if err != nil {
			return nil, err
		}
		rows := make([]Row, len(cs))
		for i, x := range cs {
			rows[i] = Row{ID: "category/" + x.Name, Label: x.Name + "  " + x.SavePath, Data: x}
		}
		return rows, setRows("category", rows)
	}
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
			return t.answer(AppInfo(ctxOf(c), cl))
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

	r.Define("torrent").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result {
			rows, err := torrentRows(ctxOf(c))
			return t.answer(rows, err)
		}, "the torrent rows: {id: hash, label: \"state  progress%  name\", data: {state, progress, size, dlspeed, eta, category, added_on, completed_on, save_path}}").Rows("torrent").
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
		h, err := hashes(c, "delete")
		if err == nil && h == "all" {
			err = errors.New("torrent delete takes --target HASH, never --all")
		}
		if err != nil {
			return ochazuke.Failed(err)
		}
		cl, err := api()
		if err != nil {
			return ochazuke.Failed(err)
		}
		files, _ := c.Args["files"].(bool)
		err = cl.Post(ctxOf(c), "/api/v2/torrents/delete", url.Values{"hashes": {h}, "deleteFiles": {strconv.FormatBool(files)}})
		what := "kept its files"
		if files {
			what = "deleted its files"
		}
		return t.answer(Message{Message: "deleted torrent " + h + ", " + what}, err)
	}, "remove --target HASH from qBittorrent; --files deletes its data too (asks first; --yes skips the question)").Confirm("Delete torrent {{label}}?")
	r.Operations["torrent.delete"] = withParams(r.Operations["torrent.delete"], params("files", ochazuke.Parameter{Type: "boolean", Description: "delete the downloaded data as well"}))

	r.Define("transfer").
		Verb("info", func(c ochazuke.Ctx) ochazuke.Result {
			cl, err := api()
			if err != nil {
				return ochazuke.Failed(err)
			}
			return t.answer(TransferInfo(ctxOf(c), cl))
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

	limitOf := func(c ochazuke.Ctx) (int64, error) {
		s := ochazuke.StringArg(c.Args, "limit", "")
		if s == "" {
			return DefaultLimit, nil
		}
		return parseSize(s)
	}
	guardVerb := func(verb, desc string, run func(ctx context.Context, c ochazuke.Ctx, lim int64) (any, error)) {
		r.Define("guard").Verb(verb, func(c ochazuke.Ctx) ochazuke.Result {
			lim, err := limitOf(c)
			if err != nil {
				return ochazuke.Failed(err)
			}
			v, err := run(ctxOf(c), c, lim)
			return t.answer(v, err)
		}, desc)
	}
	guardVerb("check", "the monitor step: df the volume over ssh; pause every torrent once free space is more than --limit below the baseline, write the marker; one line to guard.log", func(ctx context.Context, _ ochazuke.Ctx, lim int64) (any, error) {
		return t.GuardCheck(ctx, lim)
	})
	guardVerb("status", "the launchd agent, the last guard.log line, free space against the baseline and limit, the marker", func(ctx context.Context, _ ochazuke.Ctx, lim int64) (any, error) {
		return t.GuardStatusOf(ctx, lim)
	})
	guardVerb("rebase", "store the current free space as the baseline (or --bytes N) and clear the marker", func(ctx context.Context, c ochazuke.Ctx, lim int64) (any, error) {
		n := ochazuke.IntArg(c.Args, "bytes", 0)
		if n < 0 {
			return nil, errors.New("--bytes is a positive byte count")
		}
		return t.GuardRebase(ctx, int64(n), lim)
	})
	guardVerb("resume", "resume every torrent and clear the marker", func(ctx context.Context, _ ochazuke.Ctx, lim int64) (any, error) {
		return t.GuardResume(ctx, lim)
	})
	r.Define("guard").
		Verb("install", func(c ochazuke.Ctx) ochazuke.Result { return t.answer(t.GuardInstall(ctxOf(c))) },
			"write ~/Library/LaunchAgents/"+GuardLabel+".plist (o-qbt guard check every 300 s and at load), lint it, bootstrap it in gui/UID; idempotent").
		Verb("uninstall", func(c ochazuke.Ctx) ochazuke.Result { return t.answer(t.GuardUninstall(ctxOf(c))) },
			"bootout the agent and remove its plist; the baseline, marker and log stay").
		Verb("log", func(c ochazuke.Ctx) ochazuke.Result {
			return t.answer(t.GuardLogTail(ochazuke.IntArg(c.Args, "tail", 20)), nil)
		}, "the last --tail N lines of guard.log (default 20)").Positional("tail")
	for _, verb := range []string{"check", "status", "resume"} {
		r.Operations["guard."+verb] = withParams(r.Operations["guard."+verb], params("limit", limitParam))
	}
	r.Operations["guard.rebase"] = withParams(r.Operations["guard.rebase"], params("limit", limitParam,
		"bytes", ochazuke.Parameter{Type: "integer", Description: "the baseline in bytes instead of the current free space"}))
	r.Operations["guard.log"] = withParams(r.Operations["guard.log"], params("tail", ochazuke.Parameter{Type: "integer", Description: "how many lines (default 20)"}))

	r.Define("category").
		Verb("list", func(c ochazuke.Ctx) ochazuke.Result {
			rows, err := categoryRows(ctxOf(c))
			return t.answer(rows, err)
		}, "the category rows: {id: category/NAME, label: \"NAME  SAVE_PATH\", data: {name, savePath}}").Rows("category")

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
