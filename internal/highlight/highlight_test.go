package highlight

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/vt"
)

// marked is text with each span's bytes in brackets.
func marked(h *Highlighter, text string) string {
	var b strings.Builder
	last := 0
	for _, s := range h.Spans(text) {
		b.WriteString(text[last:s.From] + "[" + text[s.From:s.To] + "]")
		last = s.To
	}
	return b.String() + text[last:]
}

func TestPresets(t *testing.T) {
	h := New(nil, PresetIDs())
	for text, want := range map[string]string{
		"ERROR: connection refused":               "[ERROR]: connection [refused]",
		"no errorcode here":                       "no errorcode here",
		"Warning: deprecated":                     "[Warning]: [deprecated]",
		"[  OK  ] Started nginx, ok":              "[[]  [OK]  []] Started nginx, ok",
		"from 192.168.1.20:22 port":               "from [192.168.1.20:22] port",
		"10.0.0.0/8 and 999.1.1.1":                "[10.0.0.0]/8 and [999.1.1.1]",
		"link/ether 00:1a:2B:3c:4d:5e brd":        "link/ether [00:1a:2B:3c:4d:5e] brd",
		"inet6 fe80::1/64 scope":                  "inet6 [fe80::1/64] scope",
		"std::vector and 12:30:45":                "std::vector and [12:30:45]",
		"open https://example.com/a?b=1.":         "open [https://example.com/a?b=1].",
		"(see http://x.io/p)":                     "[(]see [http://x.io/p][)]",
		"on https://0.0.0.0:8443/error ok":        "on [https://0.0.0.0:8443/error] ok",
		"2026-11-05T09:30:00Z start":              "[2026-11-05T09:30:00Z] start",
		"Nov  5 09:30:01 host sshd":               "Nov  5 [09:30:01] host sshd",
		`"GET /a HTTP/1.1" 404 512`:               `"[GET] /a HTTP/[1.1]" [404] 512`,
		"status=503 took 200ms":                   "status=[503] took 200ms",
		"id 3f2b8c1e-9d4a-4b6f-8e2a-1c5d7f9b0a3e": "id [3f2b8c1e-9d4a-4b6f-8e2a-1c5d7f9b0a3e]",
		"commit 1234567 deadbeef":                 "commit 1234567 deadbeef",
		"错误 ERROR 中文":                             "错误 [ERROR] 中文",
	} {
		if got := marked(h, text); got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
	// A status shows in its class's color. A version beside it is its own match.
	for text, want := range map[string]struct {
		code  string
		color uint8
	}{
		"HTTP/1.1 204": {"204", green},
		"code: 301":    {"301", cyan},
		"Status=429":   {"429", yellow},
		`HTTP/2" 500`:  {"500", red},
	} {
		var color uint8
		var n int
		for _, s := range h.Spans(text) {
			if text[s.From:s.To] == want.code {
				color, n = s.Style.Color, n+1
			}
		}
		if n != 1 || color != want.color {
			t.Errorf("%q: %+v, want %s in color %d", text, h.Spans(text), want.code, want.color)
		}
	}
	if s := h.Spans("from 192.168.1.20:22 port"); len(s) != 1 || s[0].Style.Color != green {
		t.Errorf("address: %+v", s)
	}
	if got := marked(h, "sha1 2fd4e1c67a2d28fced849ee1bb76e7391b93eb12"); got != "sha1 [2fd4e1c67a2d28fced849ee1bb76e7391b93eb12]" {
		t.Errorf("hash: %q", got)
	}
	for _, p := range Presets {
		if len(New(nil, []string{p.ID}).Spans(p.Sample)) == 0 {
			t.Errorf("preset %s colors nothing of its sample", p.ID)
		}
	}
	if !New(nil, nil).Empty() {
		t.Error("rules without presets on")
	}
}

func TestShellPreset(t *testing.T) {
	h := New(nil, PresetIDs())
	text := "root@Debian-base:~# ls -al (192.168.7.3) Nov  2 15:47 7.0.14-15"
	want := map[string]uint8{
		"root@Debian-base": magenta,
		"#":                blue,
		"-al":              yellow,
		"(":                green,
		"192.168.7.3":      green,
		")":                green,
		"15:47":            green,
		"7.0.14":           green,
	}
	got := map[string]uint8{}
	for _, s := range h.Spans(text) {
		got[text[s.From:s.To]] = s.Style.Color
	}
	for part, color := range want {
		if got[part] != color {
			t.Errorf("%q color %d, spans %q", part, got[part], marked(h, text))
		}
	}
	// A prompt the shell already colored stays that color.
	ls := screen(24, 1, "\x1b[32mroot@debian:~#\x1b[0m")
	h.Apply(ls, false)
	if c := ls[0].Cells[0]; c.FG != vt.Indexed(2) {
		t.Errorf("colored prompt: %+v", c)
	}
	if c := ls[0].Cells[13]; c.Rune != '#' || c.FG != vt.Indexed(2) {
		t.Errorf("colored prompt mark: %+v", c)
	}
}

func TestRules(t *testing.T) {
	for _, c := range []struct {
		rule       Rule
		text, want string
	}{
		{Rule{Pattern: "foo"}, "Foo food", "[Foo] [foo]d"},
		{Rule{Pattern: "foo", CaseSensitive: true}, "Foo foo", "Foo [foo]"},
		{Rule{Pattern: "foo", WholeWord: true}, "foo food", "[foo] food"},
		{Rule{Pattern: "a.b"}, "axb a.b", "axb [a.b]"},
		{Rule{Pattern: `a.b`, Regex: true}, "axb", "[axb]"},
		{Rule{Pattern: `\d+`, Regex: true, WholeWord: true}, "12 a34", "[12] a34"},
		// \b only beside a word's letter: a CJK word matches whole.
		{Rule{Pattern: "失败", WholeWord: true}, "任务失败了", "任务[失败]了"},
		{Rule{Pattern: "x", Off: true}, "x", "x"},
		{Rule{Pattern: `(`, Regex: true}, "(", "("},
		{Rule{Pattern: `a*`, Regex: true}, "bbb", "bbb"},
	} {
		if got := marked(New([]Rule{c.rule}, nil), c.text); got != c.want {
			t.Errorf("%+v on %q: got %q, want %q", c.rule, c.text, got, c.want)
		}
	}
	if _, err := (Rule{Pattern: "("}).Compile(); err != nil {
		t.Errorf("text with a parenthesis: %v", err)
	}
	if _, err := (Rule{Pattern: "(", Regex: true}).Compile(); err == nil {
		t.Error("an invalid expression compiles")
	}
	if _, err := (Rule{}).Compile(); err != ErrEmpty {
		t.Errorf("empty: %v", err)
	}
	// The user's rules match first, and a match that overlaps one shown
	// does not show in part.
	h := New([]Rule{{Pattern: "connection refused", Style: Style{Color: 5}}}, PresetIDs())
	if got := marked(h, "ERROR connection refused"); got != "[ERROR] [connection refused]" {
		t.Errorf("got %q", got)
	}
	if s := h.Spans("connection refused"); len(s) != 1 || s[0].Style.Color != 5 {
		t.Errorf("spans %+v", s)
	}
}

// line is a line of cells that WriteString wrote.
func screen(cols, rows int, out string) []vt.Line {
	term := vt.New(cols, rows)
	term.WriteString(out)
	return term.Snapshot(0).Lines
}

func TestApply(t *testing.T) {
	h := New([]Rule{{Pattern: "hello world", Style: Style{Color: 2, Underline: true}}}, []string{"errors"})
	// Wide letters, a combining mark, a program's own color and a match
	// across the rows of a line that wraps.
	ls := screen(14, 4, "中文 ERROR\r\n\x1b[31mERROR\x1b[0m é ERROR\r\nxxxx hello world")
	h.Apply(ls, false)
	cell := func(y, x int) vt.Cell { return ls[y].Cells[x] }
	for x := 5; x < 10; x++ {
		if c := cell(0, x); c.FG != vt.Indexed(1) || c.Attr&vt.Bold == 0 {
			t.Errorf("row 0 col %d: %+v", x, c)
		}
	}
	if c := cell(0, 0); !c.FG.IsDefault() {
		t.Errorf("wide letter colored: %+v", c)
	}
	// The program's red stays as it was, without the rule's bold.
	if c := cell(1, 0); c.FG != vt.Indexed(1) || c.Attr&vt.Bold != 0 {
		t.Errorf("program color: %+v", c)
	}
	if c := cell(1, 8); c.FG != vt.Indexed(1) {
		t.Errorf("after a combining mark: %+v", c)
	}
	// "xxxx hello wor" | "ld": the match continues on the next row.
	if !ls[2].Wrapped {
		t.Fatal("no wrap")
	}
	for _, p := range [][2]int{{2, 5}, {2, 13}, {3, 0}, {3, 1}} {
		if c := cell(p[0], p[1]); c.FG != vt.Indexed(2) || c.Attr&vt.Underline == 0 {
			t.Errorf("wrapped match at %v: %+v", p, c)
		}
	}
	if c := cell(3, 2); !c.FG.IsDefault() {
		t.Errorf("past the match: %+v", c)
	}

	ls = screen(12, 2, "\x1b[31mERROR\x1b[0m")
	h.Apply(ls, true)
	if c := cell(0, 0); c.Attr&vt.Bold == 0 {
		t.Errorf("override: %+v", c)
	}
	bg := New([]Rule{{Pattern: "x", Style: Style{Color: 3, Background: true}}}, nil)
	ls = screen(4, 1, "axb")
	bg.Apply(ls, false)
	if c := cell(0, 1); c.BG != vt.Indexed(3) || !c.FG.IsDefault() {
		t.Errorf("background: %+v", c)
	}
}

func TestSeenBounded(t *testing.T) {
	h := New(nil, PresetIDs())
	for i := range maxSeen + 10 {
		h.Spans(fmt.Sprintf("line %d ERROR", i))
	}
	if len(h.seen) > maxSeen {
		t.Errorf("remembers %d lines", len(h.seen))
	}
	if s := h.Spans("line 1 ERROR"); len(s) != 1 {
		t.Errorf("spans after a reset %+v", s)
	}
}

func BenchmarkApply(b *testing.B) {
	h := New(nil, PresetIDs())
	var out strings.Builder
	for i := range 50 {
		fmt.Fprintf(&out, "2026-11-05 12:00:%02d INFO request from 10.0.0.%d GET /api/v1/items status=200 id=%d\r\n", i%60, i, i)
	}
	term := vt.New(150, 50)
	term.WriteString(out.String())
	var snap vt.Snapshot
	b.ReportAllocs()
	for b.Loop() {
		term.SnapshotInto(&snap, 0)
		h.Apply(snap.Lines, false)
	}
}
