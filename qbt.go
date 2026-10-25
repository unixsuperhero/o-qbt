package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// The domain: what each verb asks qBittorrent and the records it answers.
// Nothing here knows the command line.

// Torrent is a torrents/info element, the fields a row carries.
type Torrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	State       string  `json:"state"`
	Progress    float64 `json:"progress"`
	Size        int64   `json:"size"`
	DLSpeed     int64   `json:"dlspeed"`
	UPSpeed     int64   `json:"upspeed"`
	ETA         int64   `json:"eta"`
	Category    string  `json:"category"`
	AddedOn     int64   `json:"added_on"`
	CompletedOn int64   `json:"completion_on"`
	SavePath    string  `json:"save_path"`
}

// TorrentData is a torrent row's data.
type TorrentData struct {
	State       string  `json:"state"`
	Progress    float64 `json:"progress"`
	Size        int64   `json:"size"`
	DLSpeed     int64   `json:"dlspeed"`
	ETA         int64   `json:"eta"`
	Category    string  `json:"category"`
	AddedOn     int64   `json:"added_on"`
	CompletedOn int64   `json:"completed_on"`
	SavePath    string  `json:"save_path"`
}

// Row is one element of a list: id (what --target takes), label, data.
type Row struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Data  any    `json:"data,omitempty"`
}

func torrentRow(t Torrent) Row {
	return Row{
		ID:    t.Hash,
		Label: fmt.Sprintf("%s  %.1f%%  %s", t.State, t.Progress*100, t.Name),
		Data:  TorrentData{t.State, t.Progress, t.Size, t.DLSpeed, t.ETA, t.Category, t.AddedOn, t.CompletedOn, t.SavePath},
	}
}

// Torrents is every torrent, by name.
func Torrents(ctx context.Context, c *Client) ([]Torrent, error) {
	var ts []Torrent
	if err := c.Get(ctx, "/api/v2/torrents/info", nil, &ts); err != nil {
		return nil, err
	}
	sort.SliceStable(ts, func(i, j int) bool { return strings.ToLower(ts[i].Name) < strings.ToLower(ts[j].Name) })
	return ts, nil
}

// TorrentShow is one torrent's info and properties.
func TorrentShow(ctx context.Context, c *Client, hash string) (map[string]any, error) {
	var ts []map[string]any
	if err := c.Get(ctx, "/api/v2/torrents/info", url.Values{"hashes": {hash}}, &ts); err != nil {
		return nil, err
	}
	if len(ts) == 0 {
		return nil, fmt.Errorf("no torrent %s", hash)
	}
	var props map[string]any
	if err := c.Get(ctx, "/api/v2/torrents/properties", url.Values{"hash": {hash}}, &props); err != nil {
		return nil, err
	}
	return map[string]any{"info": ts[0], "properties": props}, nil
}

// Category is a torrents/categories element.
type Category struct {
	Name     string `json:"name"`
	SavePath string `json:"savePath"`
}

// Categories is every category, by name.
func Categories(ctx context.Context, c *Client) ([]Category, error) {
	m := map[string]Category{}
	if err := c.Get(ctx, "/api/v2/torrents/categories", nil, &m); err != nil {
		return nil, err
	}
	out := make([]Category, 0, len(m))
	for name, cat := range m {
		if cat.Name == "" {
			cat.Name = name
		}
		out = append(out, cat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Transfer is transfer/info.
type Transfer struct {
	DLSpeed   int64  `json:"dl_info_speed"`
	DLData    int64  `json:"dl_info_data"`
	UPSpeed   int64  `json:"up_info_speed"`
	UPData    int64  `json:"up_info_data"`
	DLLimit   int64  `json:"dl_rate_limit"`
	UPLimit   int64  `json:"up_rate_limit"`
	DHTNodes  int64  `json:"dht_nodes"`
	Status    string `json:"connection_status"`
	AltLimits *bool  `json:"use_alt_speed_limits,omitempty"`
}

func (x Transfer) Text() string {
	rows := [][2]string{
		{"download", rate(x.DLSpeed) + " (limit " + limit(x.DLLimit) + ", session " + bytesH(x.DLData) + ")"},
		{"upload", rate(x.UPSpeed) + " (limit " + limit(x.UPLimit) + ", session " + bytesH(x.UPData) + ")"},
		{"connection", x.Status},
		{"dht nodes", strconv.FormatInt(x.DHTNodes, 10)},
	}
	if x.AltLimits != nil {
		rows = append(rows, [2]string{"alt limits", map[bool]string{true: "on", false: "off"}[*x.AltLimits]})
	}
	return table(rows)
}

// TransferInfo is transfer/info plus whether the alternative limits are on.
func TransferInfo(ctx context.Context, c *Client) (Transfer, error) {
	var x Transfer
	if err := c.Get(ctx, "/api/v2/transfer/info", nil, &x); err != nil {
		return x, err
	}
	var mode string
	if err := c.Get(ctx, "/api/v2/transfer/speedLimitsMode", nil, &mode); err == nil {
		on := mode == "1"
		x.AltLimits = &on
	}
	return x, nil
}

// Info is app info: the benign credentials test.
type Info struct {
	URL       string         `json:"url"`
	User      string         `json:"user"`
	Login     string         `json:"login"`
	Version   string         `json:"version"`
	WebAPI    string         `json:"webapi_version"`
	Build     map[string]any `json:"build_info"`
	DLSpeed   int64          `json:"dl_info_speed"`
	UPSpeed   int64          `json:"up_info_speed"`
	FreeSpace int64          `json:"free_space_on_disk"`
}

func (i Info) Text() string {
	var build []string
	for _, k := range []string{"qt", "libtorrent", "boost", "openssl", "bitness"} {
		if v, ok := i.Build[k]; ok {
			build = append(build, fmt.Sprintf("%s %v", k, v))
		}
	}
	return table([][2]string{
		{"url", i.URL}, {"login", i.Login + " (" + i.User + ")"}, {"version", i.Version}, {"webapi", i.WebAPI},
		{"build", strings.Join(build, ", ")}, {"download", rate(i.DLSpeed)}, {"upload", rate(i.UPSpeed)},
		{"free disk", bytesH(i.FreeSpace)},
	})
}

// AppInfo logs in and reads version, API version, build, speeds and the
// free space qBittorrent sees; it changes nothing.
func AppInfo(ctx context.Context, c *Client) (Info, error) {
	i := Info{URL: c.Base, User: c.User}
	if err := c.Get(ctx, "/api/v2/app/version", nil, &i.Version); err != nil {
		return i, err
	}
	i.Login = "ok"
	if err := c.Get(ctx, "/api/v2/app/webapiVersion", nil, &i.WebAPI); err != nil {
		return i, err
	}
	if err := c.Get(ctx, "/api/v2/app/buildInfo", nil, &i.Build); err != nil {
		return i, err
	}
	var x Transfer
	if err := c.Get(ctx, "/api/v2/transfer/info", nil, &x); err != nil {
		return i, err
	}
	i.DLSpeed, i.UPSpeed = x.DLSpeed, x.UPSpeed
	var main struct {
		ServerState struct {
			Free int64 `json:"free_space_on_disk"`
		} `json:"server_state"`
	}
	if err := c.Get(ctx, "/api/v2/sync/maindata", url.Values{"rid": {"0"}}, &main); err != nil {
		return i, err
	}
	i.FreeSpace = main.ServerState.Free
	return i, nil
}

// prefKeys are the preferences app prefs selects.
var prefKeys = []string{"save_path", "temp_path_enabled", "temp_path", "max_active_downloads", "max_active_uploads", "max_active_torrents", "dl_limit", "up_limit", "alt_dl_limit", "alt_up_limit"}

// Prefs is the selected preferences, by key.
func Prefs(ctx context.Context, c *Client) (map[string]any, error) {
	var all map[string]json.RawMessage
	if err := c.Get(ctx, "/api/v2/app/preferences", nil, &all); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, k := range prefKeys {
		if raw, ok := all[k]; ok {
			var v any
			_ = json.Unmarshal(raw, &v)
			out[k] = v
		}
	}
	return out, nil
}

// table renders key/value rows with the keys padded to one width.
func table(rows [][2]string) string {
	w := 0
	for _, r := range rows {
		w = max(w, len(r[0]))
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%-*s  %s\n", w, r[0], r[1])
	}
	return b.String()
}

// bytesH is n bytes in binary units.
func bytesH(n int64) string {
	f, units := float64(n), []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for ; (f >= 1024 || f <= -1024) && i < len(units)-1; i++ {
		f /= 1024
	}
	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func rate(n int64) string { return bytesH(n) + "/s" }

func limit(n int64) string {
	if n <= 0 {
		return "unlimited"
	}
	return rate(n)
}
