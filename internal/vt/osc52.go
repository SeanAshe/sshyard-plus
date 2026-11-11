package vt

import (
	"bytes"
	"encoding/base64"
	"strings"
)

const (
	// oscCap is how much of an ordinary OSC is kept. OSC 52 is capped
	// by its decoded size instead, once "52;" has been seen.
	oscCap = 4096
	// OSC52MaxDefault is the decoded size of an OSC 52 paste when no
	// limit was set: 1 MiB.
	OSC52MaxDefault = 1 << 20
	// osc52MaxCeiling is the most the parser will buffer, decoded.
	osc52MaxCeiling = 8 << 20
)

// SetOSC52Max sets the largest decoded OSC 52 paste to keep. Zero uses
// the default (1 MiB). Anything above 8 MiB is capped there.
func (t *Terminal) SetOSC52Max(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.osc52Max = n
}

func (t *Terminal) osc52Limit() int {
	n := t.osc52Max
	if n <= 0 {
		n = OSC52MaxDefault
	}
	if n > osc52MaxCeiling {
		n = osc52MaxCeiling
	}
	return n
}

// osc52Sel maps Pc onto the system clipboard. c, p, s and 0–7 all do;
// an empty Pc is that clipboard too. Anything else is refused.
func osc52Sel(pc string) (string, bool) {
	if pc == "" {
		return "c", true
	}
	for i := 0; i < len(pc); i++ {
		c := pc[i]
		if c != 'c' && c != 'p' && c != 's' && (c < '0' || c > '7') {
			return "", false
		}
	}
	return pc, true
}

// b64DecodedLen is the decoded size of n base64 digits (padding not
// counted). It is what the payload would become, and an upper bound
// when the digits are not valid base64.
func b64DecodedLen(n int) int {
	if n <= 0 {
		return 0
	}
	full, rem := n/4, n%4
	out := full * 3
	if rem > 1 {
		out += rem - 1
	}
	return out
}

// decodeOSC52 decodes Pd. ok is false when the base64 is not valid.
// raw is the decoded byte count, before NULs are removed. The text is
// UTF-8 with invalid bytes replaced by U+FFFD and NULs stripped.
// shortcut: whitespace in the base64 is not stripped, so a wrapped payload is dropped; strip it if a real program wraps OSC 52.
func decodeOSC52(s string) (text string, raw int, ok bool) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		b, err = base64.RawStdEncoding.DecodeString(s)
	}
	if err != nil {
		return "", 0, false
	}
	raw = len(b)
	text = strings.ToValidUTF8(string(b), "\uFFFD")
	text = strings.ReplaceAll(text, "\x00", "")
	return text, raw, true
}

// osc52Done handles the body of OSC 52 (everything after the first
// semicolon). A read (Pd == "?") is reported and never answered.
func (t *Terminal) osc52Done(rest string) {
	pc, pd, ok := strings.Cut(rest, ";")
	if !ok {
		return
	}
	sel, ok := osc52Sel(pc)
	if !ok {
		return
	}
	if pd == "?" {
		if t.OnClipboard != nil {
			t.OnClipboard(sel, "", true)
		}
		return
	}
	if pd == "" {
		return
	}
	text, raw, ok := decodeOSC52(pd)
	if !ok {
		return
	}
	if raw > t.osc52Limit() {
		if t.OnClipboardDrop != nil {
			t.OnClipboardDrop()
		}
		return
	}
	if text == "" || t.OnClipboard == nil {
		return
	}
	t.OnClipboard(sel, text, false)
}

// osc52SelMax is the most Pc bytes an OSC 52 may carry. Pc names a few
// clipboards ("c", "cp", "s0"); a longer one is not a clipboard write.
const osc52SelMax = 32

// b64EncodedLen is the base64 length, padding included, of n bytes.
func b64EncodedLen(n int) int { return (n + 2) / 3 * 4 }

// osc52BufMax is the most an OSC 52 may buffer after its prefix: Pc,
// the ';' and a padded payload of limit decoded bytes.
func osc52BufMax(limit int) int { return osc52SelMax + 1 + b64EncodedLen(limit) }

// osc52Scan follows one OSC 52 body (Pc ; Pd) byte by byte, so the
// buffer holding it stays within osc52BufMax. Its zero value is at the
// start of Pc.
type osc52Scan struct {
	inPd   bool
	sel    int // Pc bytes
	digits int // Pd bytes that are not padding
	pad    int // '=' seen
	raw    int // Pd bytes, padding included
	// big: over the size limit, so discarded and reported. bad: the
	// padding is malformed, so discarded quietly like invalid base64.
	big, bad bool
}

func (s *osc52Scan) dropped() bool { return s.big || s.bad }

// push accounts for b and says whether to keep it. Once dropped, the
// rest is only counted, so a malformed payload that grows past the
// limit is still reported as too big.
func (s *osc52Scan) push(b byte, limit int) bool {
	if s.big {
		return false
	}
	if !s.inPd {
		if b == ';' {
			s.inPd = true
		} else if s.sel++; s.sel > osc52SelMax {
			s.big = true
		}
		return !s.dropped()
	}
	if s.raw++; s.raw > b64EncodedLen(limit) {
		s.big = true
	}
	if s.dropped() {
		return false
	}
	if b == '=' {
		// Padding is at most two '=' at the very end.
		if s.pad++; s.pad > 2 {
			s.bad = true
		}
	} else {
		if s.pad > 0 {
			s.bad = true
		}
		if s.digits++; b64DecodedLen(s.digits) > limit {
			s.big = true
		}
	}
	return !s.dropped()
}

// oscPush buffers one OSC byte. An OSC 52 that is too big or malformed
// is marked dropped and not kept; the caller still reads to the end.
func (t *Terminal) oscPush(b byte) {
	if !t.osc52 {
		if len(t.osc) >= oscCap {
			return
		}
		t.osc = append(t.osc, b)
		if bytes.HasPrefix(t.osc, []byte("52;")) {
			t.osc52 = true
			t.oscScan = osc52Scan{}
		}
		return
	}
	limit := t.osc52Limit()
	if !t.oscScan.push(b, limit) {
		t.osc = nil
		return
	}
	if len(t.osc) >= len("52;")+osc52BufMax(limit) {
		t.oscScan.big = true
		t.osc = nil
		return
	}
	t.osc = append(t.osc, b)
}

// dcsOSCKind is the OSC number at the start of a tmux passthrough
// payload, once enough bytes have arrived to know it.
func dcsOSCKind(b []byte) (kind string, done bool) {
	if len(b) == 0 {
		return "", false
	}
	if b[0] != 0x1b {
		return "", true
	}
	if len(b) < 2 {
		return "", false
	}
	if b[1] != ']' {
		return "", true
	}
	i := 2
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		i++
	}
	if i == len(b) {
		return "", i > 8
	}
	if i == 2 || (b[i] != ';' && b[i] != 0x07) {
		return "", true
	}
	return string(b[2:i]), true
}

func (t *Terminal) dcsPush(b byte) {
	if t.dcsSkip {
		return
	}
	if !t.dcsTmux {
		t.dcs = append(t.dcs, b)
		if len(t.dcs) < 5 {
			return
		}
		if bytes.Equal(t.dcs[:5], []byte("tmux;")) {
			t.dcsTmux = true
			t.dcs = t.dcs[:0]
			return
		}
		// Not tmux: ignore the rest, the way every DCS was ignored.
		t.dcsSkip = true
		t.dcs = nil
		t.state = sString
		return
	}
	if t.dcsIs52 {
		t.dcs52Push(b)
		return
	}
	t.dcs = append(t.dcs, b)
	if !t.dcsChecked {
		kind, done := dcsOSCKind(t.dcs)
		if !done {
			if len(t.dcs) > 64 {
				t.dcsSkip = true
				t.dcs = nil
			}
			return
		}
		t.dcsChecked = true
		switch kind {
		case "52":
			t.dcsIs52 = true
			t.dcsScan = osc52Scan{}
			// "ESC ] 52 BEL" has no body: what follows is the tail.
			t.dcsEnded = b == 0x07
			return
		case "0", "2":
		default:
			t.dcsSkip = true
			t.dcs = nil
			return
		}
	}
	if len(t.dcs) > oscCap {
		t.dcsSkip = true
		t.dcs = nil
	}
}

// dcs52Push buffers one byte of a passed-through OSC 52, after its
// "ESC ] 52 ;". The body is bounded like a plain OSC 52; BEL or ESC ends
// it, and at most oscCap bytes after that are kept.
func (t *Terminal) dcs52Push(b byte) {
	if t.dcsEnded {
		if t.dcsTail < oscCap {
			t.dcs = append(t.dcs, b)
			t.dcsTail++
		}
		return
	}
	if b == 0x07 || b == 0x1b {
		if t.dcsScan.dropped() {
			// Nothing of the sequence was kept; ignore the rest.
			t.dcsSkip = true
			t.dcsTooBig = t.dcsScan.big
			t.dcs = nil
			return
		}
		t.dcsEnded = true
		t.dcsTail = 1
		t.dcs = append(t.dcs, b)
		return
	}
	limit := t.osc52Limit()
	if !t.dcsScan.push(b, limit) {
		t.dcs = nil
		return
	}
	if len(t.dcs) >= len("\x1b]52;")+osc52BufMax(limit) {
		t.dcsScan.big = true
		t.dcs = nil
		return
	}
	t.dcs = append(t.dcs, b)
}

func (t *Terminal) resetDCS() {
	t.dcs = nil
	t.dcsTmux = false
	t.dcsSkip = false
	t.dcsTooBig = false
	t.dcsChecked = false
	t.dcsIs52 = false
	t.dcsScan = osc52Scan{}
	t.dcsEnded = false
	t.dcsTail = 0
}

func (t *Terminal) finishDCS() {
	inner := t.dcs
	tmux := t.dcsTmux
	tooBig := t.dcsTooBig || (t.dcsIs52 && t.dcsScan.big)
	skip := t.dcsSkip || (t.dcsIs52 && t.dcsScan.dropped())
	t.resetDCS()
	t.state = sGround
	if !tmux {
		return
	}
	if tooBig {
		if t.OnClipboardDrop != nil {
			t.OnClipboardDrop()
		}
		return
	}
	if skip || len(inner) == 0 {
		return
	}
	t.feedPassthrough(inner)
}

// feedPassthrough parses a tmux passthrough payload. Only OSC 52 and
// OSC 0/2 take effect; anything else is consumed and ignored.
func (t *Terminal) feedPassthrough(p []byte) {
	t.onlyOSC = true
	t.state = sGround
	for _, b := range p {
		t.byte(b)
	}
	t.onlyOSC = false
	t.state = sGround
}
