package main

import (
	"cmp"
	"slices"
)

// Aggregations over torrents: summary (counts and size per state) and the
// overview. Nothing here knows the command line or a format.

// StateCount is one state's torrents: how many and their total size. The
// summary's last element is the total, state "total".
type StateCount struct {
	State string `json:"state"`
	Count int    `json:"count"`
	Size  int64  `json:"size"`
}

// Summarize counts ts per state, by state name, then the total.
func Summarize(ts []Torrent) []StateCount {
	by := map[string]*StateCount{}
	total := StateCount{State: "total"}
	for _, t := range ts {
		s := by[t.State]
		if s == nil {
			s = &StateCount{State: t.State}
			by[t.State] = s
		}
		s.Count++
		s.Size += t.Size
		total.Count++
		total.Size += t.Size
	}
	out := make([]StateCount, 0, len(by)+1)
	for _, s := range by {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b StateCount) int { return cmp.Compare(a.State, b.State) })
	return append(out, total)
}

// stateGroups are the overview's headline counts: which torrent states
// each group counts. Active is not a state: it is a torrent moving data.
var stateGroups = []struct {
	Name   string
	States []string
}{
	{"seeding", []string{"uploading", "forcedUP", "stalledUP", "queuedUP"}},
	{"downloading", []string{"downloading", "forcedDL", "stalledDL", "metaDL", "forcedMetaDL", "queuedDL"}},
	{"paused", []string{"pausedDL", "pausedUP", "stoppedDL", "stoppedUP"}},
	{"errored", []string{"error", "missingFiles"}},
}

// overviewTop is how many torrents each overview top list shows.
const overviewTop = 5

// Totals is the overview's header line.
type Totals struct {
	Torrents    int        `json:"torrents"`
	Active      int        `json:"active"`
	Seeding     int        `json:"seeding"`
	Downloading int        `json:"downloading"`
	Paused      int        `json:"paused"`
	Errored     int        `json:"errored"`
	Size        int64      `json:"size"`
	DLSpeed     int64      `json:"dlspeed"`
	UPSpeed     int64      `json:"upspeed"`
	Free        int64      `json:"free"`
	Guard       GuardState `json:"guard"`
}

// GuardState is the guard as an overview shows it, without df: the marker
// and the allowance used, from qBittorrent's own free space.
type GuardState struct {
	Mode        string `json:"mode"`
	Paused      string `json:"paused"` // "" or the marker's time
	MinFree     int64  `json:"min_free"`
	ResumeAbove int64  `json:"resume_above"`
	Baseline    int64  `json:"baseline"`
	Used        int64  `json:"used"`
	Limit       int64  `json:"limit"`
}

// CategoryCount is one category's torrent count ("" = uncategorised).
type CategoryCount struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// Overview is one screen of the client: totals, counts per state, the
// fastest downloads and uploads, the oldest unfinished, and categories.
type Overview struct {
	Totals           Totals          `json:"totals"`
	States           []StateCount    `json:"states"`
	TopDownload      []Torrent       `json:"top_download"`
	TopUpload        []Torrent       `json:"top_upload"`
	OldestUnfinished []Torrent       `json:"oldest_unfinished"`
	Categories       []CategoryCount `json:"categories"`
}

// Overviews builds the overview of ts with the server state and guard.
func Overviews(ts []Torrent, st ServerState, g GuardState) Overview {
	o := Overview{States: Summarize(ts)}
	x := &o.Totals
	x.Torrents, x.DLSpeed, x.UPSpeed, x.Free, x.Guard = len(ts), st.DLSpeed, st.UPSpeed, st.Free, g
	group := map[string]*int{"seeding": &x.Seeding, "downloading": &x.Downloading, "paused": &x.Paused, "errored": &x.Errored}
	cats := map[string]int{}
	for _, t := range ts {
		x.Size += t.Size
		if t.DLSpeed > 0 || t.UPSpeed > 0 {
			x.Active++
		}
		for _, sg := range stateGroups {
			if slices.Contains(sg.States, t.State) {
				*group[sg.Name]++
			}
		}
		cats[t.Category]++
	}
	o.TopDownload = top(ts, func(t Torrent) bool { return t.DLSpeed > 0 }, func(a, b Torrent) int { return cmp.Compare(b.DLSpeed, a.DLSpeed) })
	o.TopUpload = top(ts, func(t Torrent) bool { return t.UPSpeed > 0 }, func(a, b Torrent) int { return cmp.Compare(b.UPSpeed, a.UPSpeed) })
	o.OldestUnfinished = top(ts, func(t Torrent) bool { return t.Progress < 1 }, func(a, b Torrent) int { return cmp.Compare(a.AddedOn, b.AddedOn) })
	o.Categories = []CategoryCount{}
	for name, n := range cats {
		o.Categories = append(o.Categories, CategoryCount{name, n})
	}
	slices.SortFunc(o.Categories, func(a, b CategoryCount) int {
		if (a.Category == "") != (b.Category == "") {
			return map[bool]int{true: 1, false: -1}[a.Category == ""] // uncategorised last
		}
		return cmp.Compare(a.Category, b.Category)
	})
	return o
}

// top is the first overviewTop of ts that keep, ordered by by (stable, so
// ties keep the name order).
func top(ts []Torrent, keep func(Torrent) bool, by func(a, b Torrent) int) []Torrent {
	out := []Torrent{}
	for _, t := range ts {
		if keep(t) {
			out = append(out, t)
		}
	}
	slices.SortStableFunc(out, by)
	return out[:min(len(out), overviewTop)]
}
