package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// The output engine: a Table of typed values rendered in one of Formats.
// Humanised values (sizes, speeds, eta, times) are for table and md only;
// every other format carries the raw values.

// Formats are the --format values.
var Formats = []string{"table", "json", "jsonl", "tsv", "csv", "md", "toon"}

// formatOf is the format a --out FILE's extension names, "" for none.
func formatOf(path string) string {
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	if ext == "txt" {
		return "table"
	}
	if slices.Contains(Formats, ext) && ext != "table" {
		return ext
	}
	return ""
}

// Column is one field of a table: Name is its key (the header in json,
// tsv, csv and toon, and what --columns names), Label its heading in table
// and md, Human its rendering there (nil: the raw value), Right aligns it
// right in table.
type Column struct {
	Name, Label string
	Human       func(any) string
	Right       bool
}

func (c Column) heading() string {
	if c.Label != "" {
		return c.Label
	}
	return c.Name
}

// Table is records of one kind: Name heads toon (name[N]{...}:), Rows hold
// one value per column, Default names the columns shown when --columns is
// absent (nil: all), Vertical renders one record as key/value lines in
// table and md, and Native (when set) is what json prints instead of the
// records.
type Table struct {
	Name     string
	Columns  []Column
	Default  []string
	Rows     [][]any
	Vertical bool
	Native   any
}

// Len is how many records t has.
func (t Table) Len() int { return len(t.Rows) }

// pick is t with only the named columns, in that order; none names the
// defaults.
func (t Table) pick(names []string) (Table, error) {
	if len(names) == 0 {
		names = t.Default
	}
	if len(names) == 0 {
		return t, nil
	}
	idx := make([]int, len(names))
	for i, n := range names {
		idx[i] = slices.IndexFunc(t.Columns, func(c Column) bool { return c.Name == n })
		if idx[i] < 0 {
			all := make([]string, len(t.Columns))
			for j, c := range t.Columns {
				all[j] = c.Name
			}
			return t, fmt.Errorf("--columns: %s has no column %q (columns: %s)", t.Name, n, strings.Join(all, ","))
		}
	}
	out := t
	out.Default, out.Columns, out.Rows = nil, make([]Column, len(idx)), make([][]any, len(t.Rows))
	for i, j := range idx {
		out.Columns[i] = t.Columns[j]
	}
	for r, row := range t.Rows {
		out.Rows[r] = make([]any, len(idx))
		for i, j := range idx {
			out.Rows[r][i] = row[j]
		}
	}
	return out, nil
}

// Render is t in format; columns picks and orders the columns of table,
// tsv, csv, md and toon (json and jsonl carry every field).
func (t Table) Render(format string, columns []string) (string, error) {
	switch format {
	case "json":
		if t.Native != nil {
			return indentJSON(t.Native)
		}
		lines, err := t.objects()
		if err != nil || len(lines) == 0 {
			return "[]\n", err
		}
		if t.Vertical {
			var b bytes.Buffer
			err := json.Indent(&b, []byte(lines[0]), "", "  ")
			return b.String() + "\n", err
		}
		return "[\n  " + strings.Join(lines, ",\n  ") + "\n]\n", nil
	case "jsonl":
		lines, err := t.objects()
		if err != nil || len(lines) == 0 {
			return "", err
		}
		return strings.Join(lines, "\n") + "\n", nil
	}
	p, err := t.pick(columns)
	if err != nil {
		return "", err
	}
	switch format {
	case "table":
		return p.table(), nil
	case "md":
		return p.markdown(), nil
	case "tsv":
		return p.tsv(), nil
	case "csv":
		return p.csv()
	case "toon":
		return p.toon(), nil
	}
	return "", fmt.Errorf("--format %q: one of %s", format, strings.Join(Formats, ", "))
}

// objects is each record as one compact JSON object, keys in column order.
func (t Table) objects() ([]string, error) {
	out := make([]string, len(t.Rows))
	for r, row := range t.Rows {
		var b strings.Builder
		b.WriteByte('{')
		for i, c := range t.Columns {
			k, _ := json.Marshal(c.Name)
			v, err := json.Marshal(row[i])
			if err != nil {
				return nil, err
			}
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(k)
			b.WriteByte(':')
			b.Write(v)
		}
		b.WriteByte('}')
		out[r] = b.String()
	}
	return out, nil
}

func indentJSON(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	return string(data) + "\n", err
}

// human is a value as table and md show it.
func (c Column) human(v any) string {
	if c.Human != nil {
		return c.Human(v)
	}
	return raw(v)
}

// raw is a value as tsv, csv and toon carry it.
func raw(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// table is the padded columns a person reads (two spaces apart, a header
// row), or key/value lines for a Vertical table.
func (t Table) table() string {
	cells := make([][]string, 0, len(t.Rows)+1)
	if t.Vertical {
		var kv [][2]string
		for _, row := range t.Rows {
			for i, c := range t.Columns {
				kv = append(kv, [2]string{c.heading(), c.human(row[i])})
			}
		}
		return table(kv)
	}
	head := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		head[i] = c.heading()
	}
	cells = append(cells, head)
	for _, row := range t.Rows {
		line := make([]string, len(t.Columns))
		for i, c := range t.Columns {
			line[i] = c.human(row[i])
		}
		cells = append(cells, line)
	}
	width := make([]int, len(t.Columns))
	for _, line := range cells {
		for i, s := range line {
			width[i] = max(width[i], runewidth.StringWidth(s))
		}
	}
	var b strings.Builder
	for _, line := range cells {
		var parts []string
		for i, s := range line {
			pad := strings.Repeat(" ", width[i]-runewidth.StringWidth(s))
			switch {
			case t.Columns[i].Right:
				s = pad + s
			case i < len(line)-1:
				s += pad
			}
			parts = append(parts, s)
		}
		b.WriteString(strings.Join(parts, "  ") + "\n")
	}
	return b.String()
}

// markdown is a GitHub table (| escaped), key/value for a Vertical table.
func (t Table) markdown() string {
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ") }
	var b strings.Builder
	line := func(cells []string) { b.WriteString("| " + strings.Join(cells, " | ") + " |\n") }
	if t.Vertical {
		line([]string{"field", "value"})
		line([]string{"---", "---"})
		for _, row := range t.Rows {
			for i, c := range t.Columns {
				line([]string{cell(c.heading()), cell(c.human(row[i]))})
			}
		}
		return b.String()
	}
	head, rule := make([]string, len(t.Columns)), make([]string, len(t.Columns))
	for i, c := range t.Columns {
		head[i], rule[i] = cell(c.heading()), "---"
		if c.Right {
			rule[i] = "--:"
		}
	}
	line(head)
	line(rule)
	for _, row := range t.Rows {
		cells := make([]string, len(row))
		for i, c := range t.Columns {
			cells[i] = cell(c.human(row[i]))
		}
		line(cells)
	}
	return b.String()
}

func (t Table) names() []string {
	out := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = c.Name
	}
	return out
}

// tsv is a header and a line per record, tabs; a tab or newline inside a
// value becomes a space.
func (t Table) tsv() string {
	clean := strings.NewReplacer("\t", " ", "\r", " ", "\n", " ")
	var b strings.Builder
	b.WriteString(strings.Join(t.names(), "\t") + "\n")
	for _, row := range t.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = clean.Replace(raw(v))
		}
		b.WriteString(strings.Join(cells, "\t") + "\n")
	}
	return b.String()
}

// csv is RFC 4180 with a header.
func (t Table) csv() (string, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.Write(t.names())
	for _, row := range t.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			cells[i] = raw(v)
		}
		w.Write(cells)
	}
	w.Flush()
	return b.String(), w.Error()
}

// toon is Token-Oriented Object Notation for uniform rows: a header
// NAME[N]{col,col,...}: then one line per record, two spaces in, the
// values joined by commas. A string is quoted only when it contains a
// comma, a double quote or a newline; inside quotes \ " and newline are
// \\ \" \n. Numbers and booleans are bare, a missing value is empty.
func (t Table) toon() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s[%d]{%s}:\n", t.Name, len(t.Rows), strings.Join(t.names(), ","))
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	for _, row := range t.Rows {
		cells := make([]string, len(row))
		for i, v := range row {
			s := raw(v)
			if _, ok := v.(string); ok && strings.ContainsAny(s, ",\"\n") {
				s = `"` + esc.Replace(s) + `"`
			}
			cells[i] = s
		}
		b.WriteString("  " + strings.Join(cells, ",") + "\n")
	}
	return b.String()
}

// The humanisers table and md use.

func hBytes(v any) string { return bytesH(toInt(v)) }
func hRate(v any) string  { return rate(toInt(v)) }
func hPct(v any) string   { return fmt.Sprintf("%.1f%%", toFloat(v)*100) }
func hRatio(v any) string { return fmt.Sprintf("%.2f", toFloat(v)) }

// etaInfinity is the eta qBittorrent answers for "never".
const etaInfinity = 8640000

// hETA is seconds as the two largest units: 45s, 3m 20s, 2h 10m, 4d 3h;
// qBittorrent's 8640000 is ∞.
func hETA(v any) string {
	n := toInt(v)
	if n < 0 || n >= etaInfinity {
		return "∞"
	}
	units := []struct {
		s int64
		u string
	}{{86400, "d"}, {3600, "h"}, {60, "m"}, {1, "s"}}
	for i, u := range units {
		if n >= u.s || u.s == 1 {
			out := fmt.Sprintf("%d%s", n/u.s, u.u)
			if rest := n % u.s; i+1 < len(units) && rest/units[i+1].s > 0 {
				out += fmt.Sprintf(" %d%s", rest/units[i+1].s, units[i+1].u)
			}
			return out
		}
	}
	return "0s"
}

// hTime is a Unix time in loc as 2006-01-02 15:04; zero or less is -.
func hTime(loc *time.Location) func(any) string {
	return func(v any) string {
		n := toInt(v)
		if n <= 0 {
			return "-"
		}
		return time.Unix(n, 0).In(loc).Format("2006-01-02 15:04")
	}
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return 0
}
