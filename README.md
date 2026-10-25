# o-qbt

A family tool for qBittorrent (Web API v2) on the NAS: a credentials test, torrents and categories as rows, transfer limits, and a free-space guard that pauses every torrent before a download fills the volume. `o qbt ARGS` runs it; nabe (`o tui`) shows it after it is registered.

## Install

```sh
go build -o ~/bin/o-qbt .
o qbt config init          # writes ~/.config/o-qbt/config.json (mode 600) with an empty pass
o qbt config set pass      # prompts with echo off; or: printf %s "$PW" | o qbt config set pass --stdin
o qbt app info             # the credentials test
```

`config.json` is `{url, user, pass, volume, ssh, guard}`: `http://nas:8012` (or `http://10.0.0.200:8012` on the LAN), `admin`, the password, `/volume1` and the ssh alias `nas` the guard runs `df` through. It lives in `$XDG_CONFIG_HOME/o-qbt/` when that is set. `O_QBT_URL`, `O_QBT_USER` and `O_QBT_PASS` override it. `config show` prints everything but the password (only whether one is set); `config set KEY VALUE` sets the others, `guard.mode`, `guard.minFree` and `guard.resumeAbove` included. The password is never taken from the command line.

## Ban caution

qBittorrent bans an IP after repeated failed logins. A process logs in at most once: a refused login (`login refused: check user/pass; ...`) is the command's error and every later request in that process returns it without trying again. Do not loop a command that is failing to log in; fix the password with `o qbt config set pass` and run `o qbt app info` once. A 403 on login means the IP may already be banned: wait for the ban to lapse or clear it in the web UI.

## Commands

```
o qbt app info [output] | prefs | state
o qbt torrent list | active | seeding | downloading | paused | errored | stalled   [query] [output]
o qbt torrent export --out FILE [query] [output] | summary [query] [output] | overview [output]
o qbt torrent show --target HASH
o qbt torrent pause|resume|recheck --target HASH | --all
o qbt torrent delete --target HASH [--files] [--yes]
o qbt torrent delete --hashes H1,H2 | [--state STATE] [--filter F] [--category C] [--tag T]  [--files] [--yes]
o qbt tracker list --target HASH [--full-url] [output] | summary [query] [output] | hosts [query] [output]
o qbt transfer info [output] | limit --dl N --up N | speed-limits-toggle     (bytes/s, 0 = unlimited)
o qbt guard check|status|rebase|resume [--mode floor|allowance] [--min-free 5GiB] [--resume-above 20GiB] [--limit 10G] | rebase --bytes N | install | uninstall | log [N]
o qbt category list [output]
o qbt torrents | categories | overview          (shortcuts)
o qbt config init | show | set KEY [VALUE] [--stdin]
o qbt catalog
```

`app info` changes nothing: it reads `app/version`, `app/webapiVersion`, `app/buildInfo`, `transfer/info` and `sync/maindata` and exits 0 only when the login worked. `torrent pause`/`resume` use v5's `torrents/stop`/`start` and fall back to v4's `pause`/`resume` on a 404. `torrent delete` asks `Delete torrent LABEL?` unless `--yes`. With a selection instead of `--target` (`--hashes`, or torrents/info's `--filter`/`--category`/`--tag` narrowed by `--state`, the exact qBittorrent state), it posts `torrents/delete` in batches of 200 hashes and prints `deleted N/TOTAL` on stderr when there is more than one batch.

A torrent row is `{id: HASH, label: "state  progress%  name", data: {state, progress, size, dlspeed, eta, category, added_on, completed_on, save_path}}`; a category row is `{id: "category/NAME", label, data: {name, savePath}}`. `app state` prints both lists as JSON Lines `{noun, id, label}` (the catalog's state). List-like verbs print in `--format` (below). Other verbs a person reads (`guard *`, `config *`, `transfer limit`) print a short table; with `--json '{...}'` (how a host calls) every verb prints JSON.

## Listing and exporting

The query is qBittorrent's own (`torrents/info`), passed through:

| Flag | Values |
|---|---|
| `--filter F` | `all downloading seeding completed paused stopped active inactive resumed running stalled stalled_uploading stalled_downloading errored checking moving` |
| `--category C` / `--tag T` | one category or tag (`""` = none) |
| `--sort FIELD` | `name size progress dlspeed upspeed eta ratio added_on completed_on state` (server-side; without it the list is by name) |
| `--reverse`, `--limit N` | reverse the order, keep the first N |

`torrent active|seeding|downloading|paused|errored|stalled` are aliases of `torrent list --filter` with that value (a table in `shell.go`), and take the other flags.

Output, on `torrent list|export|summary|overview`, the presets, `category list`, `transfer info` and `app info`:

| Flag | |
|---|---|
| `--format F` | `table` (the default on a terminal), `json` (the default otherwise), `jsonl`, `tsv`, `csv`, `md`, `toon` |
| `--columns a,b,c` | which columns, in order, for table/tsv/csv/md/toon (json and jsonl carry every field) |
| `--out FILE` | write instead of printing; with no `--format` the extension picks it (`.csv .tsv .md .json .jsonl .toon`, `.txt` = table, else json) |

`table` and `md` humanise sizes, speeds, eta and times (`1.2 GiB`, `3.4 MiB/s`, `2h 10m`, `∞`, `2026-10-25 12:30`); every other format carries the raw numbers. A torrent's columns are `hash name state progress size dlspeed eta` (the default) then `upspeed ratio category tags added_on completed_on save_path`. A host's `--json '{}'` without a `format` still gets the rows (`{id, label, data}`); `--json '{"format":"csv"}'` renders.

```sh
o qbt torrent seeding --sort ratio --reverse --limit 10
o qbt torrent list --category tv --format md --columns name,size,ratio
o qbt torrent export --filter completed --out done.csv
o qbt torrents --format toon | pbcopy
o qbt torrent summary            # count and size per state, total last
o qbt overview                   # one screen: totals and guard, by state, top 5 down/up, 5 oldest unfinished, categories
```

`torrent overview` (also `o qbt overview`) prints a totals line (torrents, active, seeding, downloading, paused, errored, size, speeds, the free space qBittorrent reports, the guard), the per-state table, the top 5 by download and by upload speed (moving torrents only), the 5 oldest unfinished, and the categories with their counts. `--format json` is the whole structure `{totals, states, top_download, top_upload, oldest_unfinished, categories}`; md, toon, csv, tsv and jsonl print each section in turn (md under `## name`, the others after `# name`, toon under its own header).

### TOON

`--format toon` is Token-Oriented Object Notation for uniform rows:

```
torrent[3]{hash,name,state,progress,size,dlspeed,eta}:
  aaa,alpha,downloading,0.4217,4096,3565158,60
  ccc,"Movie, The \"Cut\"",pausedDL,0.5,1288490188,0,7800
```

The rule, exactly: a header `NAME[N]{col,col,...}:` (N records, the columns in order), then one line per record, indented two spaces, its values joined by `,`. Numbers and booleans are bare (raw: bytes, bytes/s, seconds, 0..1 progress, Unix times); a missing value is empty. A string is bare unless it contains a `,`, a `"` or a newline; then it is wrapped in `"` with `\` `"` and newline written `\\` `\"` `\n`. A string that looks like a number is not quoted, so a reader types values by column.

## Trackers

`torrents/trackers?hash=H` answers one torrent's trackers; the DHT, PeX and LSD entries (`** [DHT] **` and so on) are skipped everywhere. A tracker's host is its scheme and host (`https://tracker.example.org`, `udp://open.example.net:1337`): no user info, path or query, because private trackers put the passkey there. A URL with no host is `(unparsed)`, never the URL.

- `tracker list --target HASH`: rows `{id: host, label: "host  status", data: {url, status, tier, peers, seeds, leeches, msg}}` (a host listed again is `host#2`). `data.url` is the host; `--full-url` puts the whole announce URL there and in a `url` column, for the owner's own use only. Columns `host status tier peers seeds leeches msg` (the default), `url`.
- `tracker summary [--filter F] [--category C] [--tag T]`: one row per host across the torrents the query selects, most torrents first: `host`, `torrents` (how many list it), `working` (torrents with an entry there in status 2), `not-working` (an entry in status 4 and none working), `msg sample` (the host's most common message, 60 characters).
- `tracker hosts [query]`: the distinct hosts, one per line, for piping; `--format` renders them as a table.

`summary` and `hosts` make one request per torrent, 8 at a time, with a `trackers: N/M torrents` line on stderr. Status codes: 0 disabled, 1 not contacted, 2 working, 3 updating, 4 not working.

```sh
o qbt tracker summary --filter errored --format table   # which trackers the errored torrents (error, missingFiles) use
o qbt tracker hosts --filter errored | wc -l
o qbt tracker list --target HASH
```

## The guard

`guard check` is the monitor step: it reads the volume's free space with `ssh nas df -B1 --output=avail /volume1` and applies one of two rules, `guard.mode` in `config.json` (or `--mode`).

**floor** (the default): when free space is below `minFree` (default 5 GiB; `guard.minFree`, `--min-free 5GiB`) it pauses every torrent and writes a marker. While the marker exists, later checks log `skipped-already-paused` until free space is back above `resumeAbove` (default 20 GiB, an absolute level; `guard.resumeAbove`, `--resume-above 20GiB`), when the guard resumes every torrent and clears the marker. It only resumes what it paused: torrents paused by hand (no marker) stay paused. `resumeAbove` must be above `minFree`.

**allowance** (opt-in): when the stored baseline minus the free space is more than `--limit` (default 10 GiB) it pauses every torrent and writes the marker, and stays paused until `guard resume` (resume all, clear the marker) or `guard rebase` (store the current free space, or `--bytes N`, as the baseline and clear the marker). Only this mode reads the baseline; `rebase` in floor mode stores it and says so.

```sh
o qbt config set guard.mode floor            # or allowance
o qbt config set guard.resumeAbove 30GiB
o qbt config set guard.minFree 8GiB
```

```json
"guard": {"mode": "floor", "minFree": "5GiB", "resumeAbove": "20GiB"}
```

Without a password a check logs `error:no-credentials` and exits 0. Each check, rebase and resume appends one line (GiB) to `~/.local/share/o-qbt/guard.log`:

```
2026-10-25T12:30:00-0400 mode=floor min=5.0 resume=20.0 free=577.1 baseline=584.8 used=7.7 limit=10.0 action=ok
```

`guard install` writes `~/Library/LaunchAgents/com.unixsuperhero.o-qbt-guard.plist` (runs `~/bin/o-qbt guard check` every 300 s and at load; stdout and stderr in `~/Library/Logs/o-qbt/`), lints it and bootstraps it in `gui/UID`. `guard status` shows the agent, the last log line, the mode and its thresholds (`min free`, `resume`), free space (against the baseline and limit in allowance mode) and whether the guard paused. `guard uninstall` removes the agent and keeps the data.

It replaces the earlier shell guard (`qbt-guard.sh` on the NAS, driven by `com.unixsuperhero.qbt-guard`).

## Register in nabe

```sh
MTM_SERVER=default o-nabe extension register o-qbt
```

## Development

`gofmt -l . && go vet ./... && go test -race ./... && go run . catalog | head -5`. Tests run against a fake qBittorrent (`fake_test.go`) with an injected `df` and clock; they never reach the NAS. `go test -run Catalog -update` rewrites `testdata/catalog.golden`. ochazuke and oden are local `replace`s on `~/proj/ochazuke` and `~/proj/oden`.
