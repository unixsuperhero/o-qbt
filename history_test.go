package main

import (
	"encoding/json"
	"testing"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// Every verb o-qbt defines declares its history kind (Operation.HistoryKind)
// rather than leaving it to the default, and the catalog's history is that
// kind. A new verb without one fails here.
func TestEveryVerbDeclaresItsHistory(t *testing.T) {
	tool := testTool(t, "http://127.0.0.1:1", "", nil)
	out, _, err := cli(t, tool, "catalog")
	if err != nil {
		t.Fatal(err)
	}
	var doc ocli.CatalogDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	framework := ochazuke.NewRuntime().Operations
	ops := tool.shell().Runtime.Operations
	n := 0
	for _, noun := range doc.Nouns {
		for _, v := range noun.Verbs {
			key := noun.Name + "." + v.Name
			if _, ok := framework[key]; ok {
				continue
			}
			n++
			if v.History == "" || ops[key].HistoryKind == "" || v.History != ops[key].HistoryKind {
				t.Errorf("catalog verb %s: history %q, declared %q", key, v.History, ops[key].HistoryKind)
			}
		}
	}
	if n < 30 {
		t.Fatalf("the catalog has %d o-qbt verbs, want every one", n)
	}
}

// A spot check of the kinds: reads are fetches, verbs that act on
// qBittorrent, the guard or a file are changes.
func TestVerbHistoryKinds(t *testing.T) {
	ops := testTool(t, "http://127.0.0.1:1", "", nil).shell().Runtime.Operations
	for key, want := range map[string]string{
		"app.rows": ochazuke.HistoryFetch, "torrent.list": ochazuke.HistoryFetch, "torrent.seeding": ochazuke.HistoryFetch,
		"tracker.hosts": ochazuke.HistoryFetch, "guard.status": ochazuke.HistoryFetch, "config.show": ochazuke.HistoryFetch,
		"torrent.pause": ochazuke.HistoryChange, "torrent.delete": ochazuke.HistoryChange, "torrent.export": ochazuke.HistoryChange,
		"guard.check": ochazuke.HistoryChange, "transfer.limit": ochazuke.HistoryChange, "config.set": ochazuke.HistoryChange,
	} {
		if got := ops[key].HistoryKind; got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
}
