# o-qbt

A family tool for qBittorrent (Web API v2) on the NAS: a credentials test, torrents and categories as rows, transfer limits, and a free-space guard that pauses every torrent before a download fills the volume. `o qbt ARGS` runs it; nabe (`o tui`) shows it after it is registered.

## Install

```sh
go build -o ~/bin/o-qbt .
o qbt config init          # writes ~/.config/o-qbt/config.json (mode 600) with an empty pass
o qbt config set pass      # prompts with echo off; or: printf %s "$PW" | o qbt config set pass --stdin
o qbt app info             # the credentials test
```

`config.json` is `{url, user, pass, volume, ssh}`: `http://nas:8012` (or `http://10.0.0.200:8012` on the LAN), `admin`, the password, `/volume1` and the ssh alias `nas` the guard runs `df` through. It lives in `$XDG_CONFIG_HOME/o-qbt/` when that is set. `O_QBT_URL`, `O_QBT_USER` and `O_QBT_PASS` override it. `config show` prints everything but the password (only whether one is set); `config set KEY VALUE` sets the others. The password is never taken from the command line.

## Ban caution

qBittorrent bans an IP after repeated failed logins. A process logs in at most once: a refused login (`login refused: check user/pass; ...`) is the command's error and every later request in that process returns it without trying again. Do not loop a command that is failing to log in; fix the password with `o qbt config set pass` and run `o qbt app info` once. A 403 on login means the IP may already be banned: wait for the ban to lapse or clear it in the web UI.

## Commands

```
o qbt app info | prefs | state
o qbt torrent list | show --target HASH
o qbt torrent pause|resume|recheck --target HASH | --all
o qbt torrent delete --target HASH [--files] [--yes]
o qbt transfer info | limit --dl N --up N | speed-limits-toggle     (bytes/s, 0 = unlimited)
o qbt guard check|status|rebase|resume [--limit 10G] | rebase --bytes N | install | uninstall | log [N]
o qbt category list
o qbt config init | show | set KEY [VALUE] [--stdin]
o qbt catalog
```

`app info` changes nothing: it reads `app/version`, `app/webapiVersion`, `app/buildInfo`, `transfer/info` and `sync/maindata` and exits 0 only when the login worked. `torrent pause`/`resume` use v5's `torrents/stop`/`start` and fall back to v4's `pause`/`resume` on a 404. `torrent delete` asks `Delete torrent LABEL?` unless `--yes`.

A torrent row is `{id: HASH, label: "state  progress%  name", data: {state, progress, size, dlspeed, eta, category, added_on, completed_on, save_path}}`; a category row is `{id: "category/NAME", label, data: {name, savePath}}`. `app state` prints both lists as JSON Lines `{noun, id, label}` (the catalog's state). Verbs a person reads (`app info`, `transfer info`, `guard *`, `config *`) print a short table; with `--json '{...}'` (how a host calls) every verb prints JSON.

## The guard

`guard check` is the monitor step. It reads the baseline, runs `ssh nas df -B1 --output=avail /volume1`, and when the baseline minus the free space is more than `--limit` (default 10 GiB) it pauses every torrent and writes a marker; later checks log `skipped-already-paused` until `guard resume` (resume all, clear the marker) or `guard rebase` (store the current free space, or `--bytes N`, as the baseline and clear the marker). Without a password it logs `error:no-credentials` and exits 0. Each check, rebase and resume appends one line to `~/.local/share/o-qbt/guard.log`:

```
2026-10-25T12:30:00-0400 free=577.1 baseline=584.8 used=7.7 limit=10.0 action=ok
```

`guard install` writes `~/Library/LaunchAgents/com.unixsuperhero.o-qbt-guard.plist` (runs `~/bin/o-qbt guard check` every 300 s and at load; stdout and stderr in `~/Library/Logs/o-qbt/`), lints it and bootstraps it in `gui/UID`. `guard status` shows the agent, the last log line, free space against the baseline and the marker. `guard uninstall` removes the agent and keeps the data.

It replaces the earlier shell guard (`qbt-guard.sh` on the NAS, driven by `com.unixsuperhero.qbt-guard`).

## Register in nabe

```sh
MTM_SERVER=default o-nabe extension register o-qbt
```

## Development

`gofmt -l . && go vet ./... && go test -race ./... && go run . catalog | head -5`. Tests run against a fake qBittorrent (`fake_test.go`) with an injected `df` and clock; they never reach the NAS. `go test -run Catalog -update` rewrites `testdata/catalog.golden`. ochazuke and oden are local `replace`s on `~/proj/ochazuke` and `~/proj/oden`.
