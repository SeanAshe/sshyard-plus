package vt

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// tmuxWrap is ESC P tmux ; <payload with ESC doubled> ST.
func tmuxWrap(inner string) string {
	var b strings.Builder
	b.WriteString("\x1bPtmux;")
	for i := 0; i < len(inner); i++ {
		if inner[i] == 0x1b {
			b.WriteByte(0x1b)
		}
		b.WriteByte(inner[i])
	}
	b.WriteString("\x1b\\")
	return b.String()
}

type clip struct {
	sel   string
	data  string
	query bool
}

func collect(t *testing.T) (*Terminal, *[]clip, *int) {
	t.Helper()
	term := New(40, 6)
	term.SetOSC52Max(OSC52MaxDefault)
	var got []clip
	var drops int
	term.OnClipboard = func(sel, data string, query bool) {
		got = append(got, clip{sel, data, query})
	}
	term.OnClipboardDrop = func() { drops++ }
	return term, &got, &drops
}

func TestOSC52BELAndST(t *testing.T) {
	for _, end := range []string{"\x07", "\x1b\\"} {
		term, got, drops := collect(t)
		term.WriteString("A\x1b]52;c;" + b64("hello") + end + "Z")
		if len(*got) != 1 || (*got)[0].data != "hello" || (*got)[0].query || (*got)[0].sel != "c" {
			t.Fatalf("end %q: %+v", end, *got)
		}
		if *drops != 0 {
			t.Fatalf("dropped a short write")
		}
		if !strings.Contains(term.ScreenText(), "AZ") {
			t.Fatalf("screen %q", term.ScreenText())
		}
	}
	// No padding is still the same text.
	term, got, _ := collect(t)
	term.WriteString("\x1b]52;c;aGVsbG8\x07")
	if len(*got) != 1 || (*got)[0].data != "hello" {
		t.Fatalf("unpadded %+v", *got)
	}
}

func TestOSC52Selections(t *testing.T) {
	for _, pc := range []string{"c", "p", "s", "0", "1", "2", "3", "4", "5", "6", "7", "cp"} {
		term, got, _ := collect(t)
		term.WriteString("\x1b]52;" + pc + ";" + b64("x") + "\x07")
		if len(*got) != 1 || (*got)[0].sel != pc || (*got)[0].data != "x" {
			t.Fatalf("Pc %q: %+v", pc, *got)
		}
	}
	// An empty Pc is the system clipboard too.
	term, got, _ := collect(t)
	term.WriteString("\x1b]52;;" + b64("x") + "\x07")
	if len(*got) != 1 || (*got)[0].sel != "c" || (*got)[0].data != "x" {
		t.Fatalf("empty Pc: %+v", *got)
	}
	term, got, _ = collect(t)
	term.WriteString("\x1b]52;q;" + b64("x") + "\x07OK")
	if len(*got) != 0 || !strings.Contains(term.ScreenText(), "OK") {
		t.Fatalf("unknown Pc wrote %+v screen %q", *got, term.ScreenText())
	}
}

func TestOSC52TextCleaning(t *testing.T) {
	term, got, _ := collect(t)
	raw := []byte{'a', 0, 'b', 0xff, 'c'}
	term.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString(raw) + "\x07")
	if len(*got) != 1 || (*got)[0].data != "ab\uFFFDc" {
		t.Fatalf("%+v", *got)
	}
	// Only NULs is a clear, which is ignored.
	term, got, _ = collect(t)
	term.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte{0, 0}) + "\x07")
	if len(*got) != 0 {
		t.Fatalf("nul-only wrote %+v", *got)
	}
	// An empty payload clears the clipboard; ignore it.
	term, got, _ = collect(t)
	term.WriteString("\x1b]52;c;\x07")
	if len(*got) != 0 {
		t.Fatalf("empty payload %+v", *got)
	}
}

func TestOSC52InvalidBase64(t *testing.T) {
	term, got, drops := collect(t)
	term.WriteString("\x1b]52;c;!!!!\x07NEXT")
	if len(*got) != 0 || *drops != 0 {
		t.Fatalf("invalid base64 got %+v drops %d", *got, *drops)
	}
	if !strings.Contains(term.ScreenText(), "NEXT") {
		t.Fatalf("screen %q", term.ScreenText())
	}
}

func TestOSC52QueryNeverReplies(t *testing.T) {
	term, got, _ := collect(t)
	var replies []string
	term.Reply = func(b []byte) { replies = append(replies, string(b)) }
	term.WriteString("\x1b]52;c;?\x07\x1b]52;p;?\x1b\\AFTER")
	if len(*got) != 2 || !(*got)[0].query || !(*got)[1].query || (*got)[0].data != "" || (*got)[0].sel != "c" || (*got)[1].sel != "p" {
		t.Fatalf("%+v", *got)
	}
	if len(replies) != 0 {
		t.Fatalf("replied %q", replies)
	}
	if strings.Contains(term.ScreenText(), "?") || !strings.Contains(term.ScreenText(), "AFTER") {
		t.Fatalf("screen %q", term.ScreenText())
	}
}

func TestOSC52SizeLimit(t *testing.T) {
	term, got, drops := collect(t)
	ok := bytes.Repeat([]byte("B"), 900<<10)
	term.WriteString("PRE\x1b]52;c;" + base64.StdEncoding.EncodeToString(ok) + "\x07MID")
	if *drops != 0 || len(*got) != 1 || (*got)[0].data != string(ok) {
		t.Fatalf("900 KiB: drops %d writes %d len %d", *drops, len(*got), clipLen(*got))
	}
	if !strings.Contains(term.ScreenText(), "PRE") || !strings.Contains(term.ScreenText(), "MID") {
		t.Fatalf("screen %q", term.ScreenText())
	}

	big := bytes.Repeat([]byte("A"), 2<<20)
	term.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString(big) + "\x07POST")
	if *drops != 1 || len(*got) != 1 {
		t.Fatalf("2 MiB: drops %d writes %d", *drops, len(*got))
	}
	if !strings.Contains(term.ScreenText(), "POST") || strings.Contains(term.ScreenText(), "QUFB") {
		t.Fatalf("screen after drop %q", term.ScreenText())
	}
	// A later short write still works, so the parser is back in ground.
	term.WriteString("\x1b]52;c;" + b64("z") + "\x07")
	if len(*got) != 2 || (*got)[1].data != "z" {
		t.Fatalf("after drop %+v", *got)
	}
}

func clipLen(g []clip) int {
	if len(g) == 0 {
		return 0
	}
	return len(g[0].data)
}

func TestOSC52TmuxPassthrough(t *testing.T) {
	term, got, drops := collect(t)
	term.WriteString("\x1b]7;file://box/home\x07")
	if term.Cwd() != "/home" {
		t.Fatalf("cwd %q", term.Cwd())
	}
	// The spec's shape: inner OSC ends with BEL.
	term.WriteString("X" + tmuxWrap("\x1b]52;c;"+b64("via-tmux")+"\x07") + "Y")
	if len(*got) != 1 || (*got)[0].data != "via-tmux" || *drops != 0 {
		t.Fatalf("bel passthrough %+v drops %d", *got, *drops)
	}
	if strings.Contains(term.ScreenText(), "tmux") || strings.Contains(term.ScreenText(), "via") || !strings.Contains(term.ScreenText(), "XY") {
		t.Fatalf("screen %q", term.ScreenText())
	}
	// Inner ST: ESC in the payload is doubled, then the DCS ends.
	term.WriteString(tmuxWrap("\x1b]52;c;" + b64("st") + "\x1b\\"))
	if len(*got) != 2 || (*got)[1].data != "st" {
		t.Fatalf("st passthrough %+v", *got)
	}
	// A title is allowed through. A directory report is not, nor plain text.
	term.WriteString(tmuxWrap("\x1b]0;from-tmux\x07"))
	if term.Title() != "from-tmux" {
		t.Fatalf("title %q", term.Title())
	}
	term.WriteString(tmuxWrap("\x1b]7;file://box/tmp\x07"))
	if term.Cwd() != "/home" {
		t.Fatalf("inner osc 7 changed cwd to %q", term.Cwd())
	}
	term.WriteString(tmuxWrap("\x1b]8;;http://example\x07nope\x1b]8;;\x07"))
	if strings.Contains(term.ScreenText(), "nope") || strings.Contains(term.ScreenText(), "example") {
		t.Fatalf("inner osc 8 leaked %q", term.ScreenText())
	}
	term.WriteString(tmuxWrap("hello"))
	if strings.Contains(term.ScreenText(), "hello") {
		t.Fatalf("inner text leaked %q", term.ScreenText())
	}

	// Over the limit inside a passthrough is dropped, and the screen continues.
	term.SetOSC52Max(8)
	term.WriteString(tmuxWrap("\x1b]52;c;"+b64("hello world")+"\x07") + "OK")
	if *drops != 1 || len(*got) != 2 {
		t.Fatalf("tmux too big drops %d writes %d", *drops, len(*got))
	}
	if !strings.Contains(term.ScreenText(), "OK") {
		t.Fatalf("screen %q", term.ScreenText())
	}
}

// Malformed OSC 52 must stay within a small fixed buffer however much
// arrives, in chunks too, be reported once at its end, and leave the
// parser able to print what follows.
func TestOSC52MalformedStaysBounded(t *testing.T) {
	const n = 2 << 20
	// The most either buffer may hold with SetOSC52Max(8): a prefix, Pc
	// and a 12-byte payload.
	const bound = 64
	cases := []struct {
		name, head string
		fill       byte
		end        string
		dcs        bool
	}{
		{"osc-padding", "\x1b]52;c;", '=', "\x07", false},
		{"osc-selection", "\x1b]52;", 'c', "\x1b\\", false},
		{"osc-digits", "\x1b]52;c;", 'A', "\x07", false},
		{"tmux-selection", "\x1bPtmux;\x1b\x1b]52;", 'c', "\x1b\\", true},
		{"tmux-padding", "\x1bPtmux;\x1b\x1b]52;c;", '=', "\x1b\\", true},
		{"tmux-digits", "\x1bPtmux;\x1b\x1b]52;c;", 'A', "\x07\x1b\\", true},
	}
	for _, tc := range cases {
		term, got, drops := collect(t)
		term.SetOSC52Max(8)
		in := tc.head + strings.Repeat(string(tc.fill), n) + tc.end
		// An odd chunk size splits the prefix and the terminator too.
		for i := 0; i < len(in); i += 4093 {
			end := min(i+4093, len(in))
			term.Write([]byte(in[i:end]))
			if len(term.osc) > bound || cap(term.osc) > oscCap || len(term.dcs) > bound || cap(term.dcs) > oscCap {
				t.Fatalf("%s at %d: osc %d/%d dcs %d/%d", tc.name, end, len(term.osc), cap(term.osc), len(term.dcs), cap(term.dcs))
			}
			if end >= len(tc.head)+bound && end < len(tc.head)+n {
				dropped := term.osc52 && term.oscScan.dropped()
				if tc.dcs {
					dropped = term.dcsIs52 && term.dcsScan.dropped()
				}
				if !dropped {
					t.Fatalf("%s at %d: not dropped", tc.name, end)
				}
			}
		}
		if *drops != 1 || len(*got) != 0 {
			t.Fatalf("%s: drops %d writes %+v", tc.name, *drops, *got)
		}
		if term.state != sGround || term.osc52 || term.dcsIs52 || term.dcs != nil {
			t.Fatalf("%s: parser not reset, state %d", tc.name, term.state)
		}
		term.WriteString("AFTER")
		if !strings.Contains(term.ScreenText(), "AFTER") || strings.Contains(term.ScreenText(), string(tc.fill)+string(tc.fill)) {
			t.Fatalf("%s: screen %q", tc.name, term.ScreenText())
		}
		term.WriteString("\x1b]52;c;" + b64("ok") + "\x07" + tmuxWrap("\x1b]52;c;"+b64("tm")+"\x07"))
		if len(*got) != 2 || (*got)[0].data != "ok" || (*got)[1].data != "tm" {
			t.Fatalf("%s: later writes %+v", tc.name, *got)
		}
	}
}

// Short malformed padding is ignored quietly, like invalid base64.
func TestOSC52MalformedPadding(t *testing.T) {
	for _, pd := range []string{"YQ===", "YQ==YQ==", "YQ=Q"} {
		for _, wrap := range []bool{false, true} {
			term, got, drops := collect(t)
			seq := "\x1b]52;c;" + pd + "\x07"
			if wrap {
				seq = tmuxWrap(seq)
			}
			term.WriteString(seq + "NEXT")
			if len(*got) != 0 || *drops != 0 {
				t.Fatalf("%q tmux=%v: writes %+v drops %d", pd, wrap, *got, *drops)
			}
			if !strings.Contains(term.ScreenText(), "NEXT") {
				t.Fatalf("%q tmux=%v: screen %q", pd, wrap, term.ScreenText())
			}
		}
	}
}

// A padded payload right at the limit still copies, directly and
// through tmux, one past it is dropped, and a query carries nothing.
func TestOSC52AtLimitAndQuery(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		for _, end := range []string{"\x07", "\x1b\\"} {
			term, got, drops := collect(t)
			term.SetOSC52Max(8)
			w := func(s string) {
				if wrap {
					s = tmuxWrap(s)
				}
				term.WriteString(s)
			}
			w("\x1b]52;cp;" + b64("1234567") + end) // padded with '='
			w("\x1b]52;c;" + b64("12345678") + end) // exactly the limit
			w("\x1b]52;c;" + b64("123456789") + end)
			w("\x1b]52;c;?" + end)
			if len(*got) != 3 || (*got)[0].data != "1234567" || (*got)[0].sel != "cp" || (*got)[1].data != "12345678" {
				t.Fatalf("tmux=%v end %q: %+v", wrap, end, *got)
			}
			if q := (*got)[2]; !q.query || q.data != "" {
				t.Fatalf("tmux=%v end %q: query %+v", wrap, end, q)
			}
			if *drops != 1 {
				t.Fatalf("tmux=%v end %q: drops %d", wrap, end, *drops)
			}
		}
	}
}
