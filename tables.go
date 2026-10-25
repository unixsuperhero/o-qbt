package main

import (
	"fmt"
	"strings"
	"time"
)

// Each record type as a Table: its columns (data) and how a record fills a
// row. A list verb renders through these; nothing here asks qBittorrent.

// torrentColumns are a torrent's columns; the first seven are the default.
func torrentColumns(loc *time.Location) []Column {
	return []Column{
		{Name: "hash"}, {Name: "name"}, {Name: "state"},
		{Name: "progress", Human: hPct, Right: true},
		{Name: "size", Human: hBytes, Right: true},
		{Name: "dlspeed", Human: hRate, Right: true},
		{Name: "eta", Human: hETA, Right: true},
		{Name: "upspeed", Human: hRate, Right: true},
		{Name: "ratio", Human: hRatio, Right: true},
		{Name: "category"}, {Name: "tags"},
		{Name: "added_on", Human: hTime(loc)},
		{Name: "completed_on", Human: hTime(loc)},
		{Name: "save_path"},
	}
}

var torrentDefault = []string{"hash", "name", "state", "progress", "size", "dlspeed", "eta"}

func torrentValues(t Torrent) []any {
	return []any{t.Hash, t.Name, t.State, t.Progress, t.Size, t.DLSpeed, t.ETA, t.UPSpeed, t.Ratio, t.Category, t.Tags, t.AddedOn, t.CompletedOn, t.SavePath}
}

// TorrentTable is ts as table "torrent".
func TorrentTable(ts []Torrent, loc *time.Location) Table {
	t := Table{Name: "torrent", Columns: torrentColumns(loc), Default: torrentDefault, Rows: make([][]any, len(ts))}
	for i, x := range ts {
		t.Rows[i] = torrentValues(x)
	}
	return t
}

// CategoryTable is cs as table "category".
func CategoryTable(cs []Category) Table {
	t := Table{Name: "category", Columns: []Column{{Name: "name"}, {Name: "save_path"}}, Rows: make([][]any, len(cs))}
	for i, c := range cs {
		t.Rows[i] = []any{c.Name, c.SavePath}
	}
	return t
}

// SummaryTable is counts and size per state, the total last.
func SummaryTable(ss []StateCount) Table {
	t := Table{Name: "summary", Columns: []Column{{Name: "state"}, {Name: "count", Right: true}, {Name: "size", Human: hBytes, Right: true}}, Rows: make([][]any, len(ss))}
	for i, s := range ss {
		t.Rows[i] = []any{s.State, s.Count, s.Size}
	}
	return t
}

// InfoTable is app info as one record; json prints the Info itself.
func InfoTable(i Info) Table {
	var build []string
	for _, k := range []string{"qt", "libtorrent", "boost", "openssl", "bitness"} {
		if v, ok := i.Build[k]; ok {
			build = append(build, fmt.Sprintf("%s %v", k, v))
		}
	}
	return Table{Name: "app", Vertical: true, Native: i, Columns: []Column{
		{Name: "url"}, {Name: "user"}, {Name: "login"}, {Name: "version"}, {Name: "webapi_version", Label: "webapi"},
		{Name: "build"}, {Name: "dl_info_speed", Label: "download", Human: hRate}, {Name: "up_info_speed", Label: "upload", Human: hRate},
		{Name: "free_space_on_disk", Label: "free disk", Human: hBytes},
	}, Rows: [][]any{{i.URL, i.User, i.Login, i.Version, i.WebAPI, strings.Join(build, ", "), i.DLSpeed, i.UPSpeed, i.FreeSpace}}}
}

// TransferTable is transfer info as one record; json prints the Transfer.
func TransferTable(x Transfer) Table {
	alt := any(nil)
	if x.AltLimits != nil {
		alt = *x.AltLimits
	}
	hLimit := func(v any) string { return limit(toInt(v)) }
	hAlt := func(v any) string {
		if b, ok := v.(bool); ok {
			return map[bool]string{true: "on", false: "off"}[b]
		}
		return "-"
	}
	return Table{Name: "transfer", Vertical: true, Native: x, Columns: []Column{
		{Name: "dl_info_speed", Label: "download", Human: hRate}, {Name: "dl_rate_limit", Label: "dl limit", Human: hLimit}, {Name: "dl_info_data", Label: "dl session", Human: hBytes},
		{Name: "up_info_speed", Label: "upload", Human: hRate}, {Name: "up_rate_limit", Label: "up limit", Human: hLimit}, {Name: "up_info_data", Label: "up session", Human: hBytes},
		{Name: "connection_status", Label: "connection"}, {Name: "dht_nodes", Label: "dht nodes"}, {Name: "use_alt_speed_limits", Label: "alt limits", Human: hAlt},
	}, Rows: [][]any{{x.DLSpeed, x.DLLimit, x.DLData, x.UPSpeed, x.UPLimit, x.UPData, x.Status, x.DHTNodes, alt}}}
}

// OverviewView renders an Overview: table is one screen (a totals line,
// the per-state table, the top lists, a categories line); json is the
// Overview; every other format is its sections in turn.
type OverviewView struct {
	O   Overview
	Loc *time.Location
}

func (v OverviewView) totals() Table {
	x := v.O.Totals
	return Table{Name: "totals", Vertical: true, Columns: []Column{
		{Name: "torrents"}, {Name: "active"}, {Name: "seeding"}, {Name: "downloading"}, {Name: "paused"}, {Name: "errored"},
		{Name: "size", Human: hBytes}, {Name: "dlspeed", Label: "down", Human: hRate}, {Name: "upspeed", Label: "up", Human: hRate},
		{Name: "free", Human: hBytes}, {Name: "guard_mode"}, {Name: "guard_paused"}, {Name: "guard_min_free", Human: hBytes}, {Name: "guard_resume_above", Human: hBytes},
		{Name: "guard_baseline", Human: hBytes}, {Name: "guard_used", Human: hBytes}, {Name: "guard_limit", Human: hBytes},
	}, Rows: [][]any{{x.Torrents, x.Active, x.Seeding, x.Downloading, x.Paused, x.Errored, x.Size, x.DLSpeed, x.UPSpeed, x.Free, x.Guard.Mode, x.Guard.Paused, x.Guard.MinFree, x.Guard.ResumeAbove, x.Guard.Baseline, x.Guard.Used, x.Guard.Limit}}}
}

// sections are the overview's tables, named as json names them.
func (v OverviewView) sections() []Table {
	pick := func(name string, ts []Torrent, cols ...string) Table {
		t, _ := TorrentTable(ts, v.Loc).pick(cols)
		t.Name = name
		return t
	}
	cats := Table{Name: "categories", Columns: []Column{{Name: "category"}, {Name: "count"}}}
	for _, c := range v.O.Categories {
		cats.Rows = append(cats.Rows, []any{c.Category, c.Count})
	}
	states := SummaryTable(v.O.States)
	states.Name = "states"
	return []Table{
		v.totals(), states,
		pick("top_download", v.O.TopDownload, "name", "dlspeed", "progress", "eta"),
		pick("top_upload", v.O.TopUpload, "name", "upspeed", "ratio"),
		pick("oldest_unfinished", v.O.OldestUnfinished, "name", "added_on", "progress", "state"),
		cats,
	}
}

// line is the guard as the overview's header says it.
func (g GuardState) line() string {
	state := "ok"
	if g.Paused != "" {
		state = "paused since " + g.Paused
	}
	if g.Mode == "floor" {
		return fmt.Sprintf("guard floor: %s (pause below %s, resume above %s)", state, bytesH(g.MinFree), bytesH(g.ResumeAbove))
	}
	if g.Baseline == 0 {
		return "guard allowance: no baseline (o qbt guard rebase)"
	}
	return fmt.Sprintf("guard allowance: %s, %s of %s used", state, bytesH(g.Used), bytesH(g.Limit))
}

func (v OverviewView) Render(format string, columns []string) (string, error) {
	if len(columns) > 0 {
		return "", fmt.Errorf("--columns does not apply to torrent overview")
	}
	switch format {
	case "json":
		return indentJSON(v.O)
	case "table":
		x := v.O.Totals
		var b strings.Builder
		fmt.Fprintf(&b, "torrents %d  active %d  seeding %d  downloading %d  paused %d  errored %d  size %s  down %s  up %s  free %s  %s\n",
			x.Torrents, x.Active, x.Seeding, x.Downloading, x.Paused, x.Errored, bytesH(x.Size), rate(x.DLSpeed), rate(x.UPSpeed), bytesH(x.Free), x.Guard.line())
		titles := map[string]string{"states": "by state", "top_download": "top download", "top_upload": "top upload", "oldest_unfinished": "oldest unfinished"}
		secs := v.sections()
		for _, t := range secs[1 : len(secs)-1] {
			b.WriteString("\n" + titles[t.Name] + "\n")
			if t.Len() == 0 {
				b.WriteString("  (none)\n")
				continue
			}
			s, _ := t.Render("table", nil)
			b.WriteString(s)
		}
		var cats []string
		for _, c := range v.O.Categories {
			name := c.Category
			if name == "" {
				name = "(none)"
			}
			cats = append(cats, fmt.Sprintf("%s %d", name, c.Count))
		}
		b.WriteString("\ncategories: " + strings.Join(cats, ", ") + "\n")
		return b.String(), nil
	}
	var parts []string
	for _, t := range v.sections() {
		s, err := t.Render(format, nil)
		if err != nil {
			return "", err
		}
		switch format {
		case "md":
			s = "## " + t.Name + "\n\n" + s
		case "toon":
		default:
			s = "# " + t.Name + "\n" + s
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n"), nil
}
