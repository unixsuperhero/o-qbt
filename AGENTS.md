# o-qbt: agent guide

The qBittorrent family tool (`o-qbt`, so `o qbt`). An oden app with an ochazuke runtime behind `ochazuke/cli`. Read `README.md`, then `../ochazuke/AGENTS.md` ("Building an application on ochazuke") and `../o-ai/{main.go,shell.go}`, the sibling it copies.

| Concern | File |
|---|---|
| `Tool`, `main`, the oden app, mount, the person/JSON output switch (`runShell`, `Texter`) | `main.go` |
| The runtime: nouns, verbs, parameters, rows, `Reveal`, `app state` | `shell.go` |
| Settings file, env overrides, the password prompt | `config.go` |
| The Web API session: one login per process, stop/pause fallback | `client.go` |
| What each verb asks qBittorrent; records and tables | `qbt.go` |
| Aggregations: summary per state, overview | `report.go` |
| The output engine: `Table`, formats (table, json, jsonl, tsv, csv, md, toon), humanisers | `format.go` |
| Each record type as a `Table`; the overview's rendering | `tables.go` |
| The guard: floor and allowance modes, baseline, marker, log, df over ssh, launchd agent | `guard.go` |
| Fake qBittorrent and the test tool; output goldens in `testdata/` (`go test -update`) | `fake_test.go` |

## Plans and todos

Family plans, todos and the roadmap live in `~/hub/o-plans/` (`README.md` is the index). Read it before planning work; add or update items there. Do not add a TODO.md or docs/plans/ to this repo.

## Invariants

- Never retry a failed login: qBittorrent bans the IP. `Client.login` runs once per process; tests pin it.
- Tests never reach the NAS or a real qBittorrent: `testTool` injects the URL, `df`, the clock and external commands.
- The password is never printed and never read from argv.
- Every list verb declares `.Rows(noun)`; `testdata/catalog.golden` pins the catalog.
- Every verb declares its history kind: `.Fetch()` on a read, `setHistory(r, ochazuke.HistoryChange, ...)` on a verb that acts on qBittorrent, the guard or a file; `TestEveryVerbDeclaresItsHistory` fails on one left to the default.
- Never bind function keys.

## Every commit

`gofmt -l . && go vet ./... && timeout 300 go test -race -count=2 ./... && go run . catalog | head -5`, and `o proj check`. Conventional commits on main, never amend.
