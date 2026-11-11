package vt

import (
	"fmt"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// OutputEncoding selects how printable program output is decoded.
type OutputEncoding string

const (
	EncodingUTF8        OutputEncoding = "UTF-8"
	EncodingGBK         OutputEncoding = "GBK"
	EncodingGB2312      OutputEncoding = "GB2312"
	EncodingGB18030     OutputEncoding = "GB18030"
	EncodingBig5        OutputEncoding = "Big5"
	EncodingShiftJIS    OutputEncoding = "Shift_JIS"
	EncodingEUCJP       OutputEncoding = "EUC-JP"
	EncodingEUCKR       OutputEncoding = "EUC-KR"
	EncodingWindows1252 OutputEncoding = "Windows-1252"
	EncodingWindows1250 OutputEncoding = "Windows-1250"
	EncodingISO8859_1   OutputEncoding = "ISO-8859-1"
	EncodingISO8859_2   OutputEncoding = "ISO-8859-2"
	EncodingISO8859_15  OutputEncoding = "ISO-8859-15"
	EncodingWindows1251 OutputEncoding = "Windows-1251"
	EncodingKOI8R       OutputEncoding = "KOI8-R"
	EncodingKOI8U       OutputEncoding = "KOI8-U"
	EncodingISO8859_5   OutputEncoding = "ISO-8859-5"
	EncodingCP437       OutputEncoding = "CP437"
	EncodingCP850       OutputEncoding = "CP850"
	EncodingCP866       OutputEncoding = "CP866"
)

// GB2312 deliberately uses the GBK-compatible decoder, including its extensions.
var outputEncodings = [...]struct {
	id    OutputEncoding
	codec encoding.Encoding
}{
	{EncodingUTF8, nil},
	{EncodingGB2312, simplifiedchinese.GBK},
	{EncodingGBK, simplifiedchinese.GBK},
	{EncodingGB18030, simplifiedchinese.GB18030},
	{EncodingBig5, traditionalchinese.Big5},
	{EncodingShiftJIS, japanese.ShiftJIS},
	{EncodingEUCJP, japanese.EUCJP},
	{EncodingEUCKR, korean.EUCKR},
	{EncodingWindows1252, charmap.Windows1252},
	{EncodingWindows1250, charmap.Windows1250},
	{EncodingISO8859_1, charmap.ISO8859_1},
	{EncodingISO8859_2, charmap.ISO8859_2},
	{EncodingISO8859_15, charmap.ISO8859_15},
	{EncodingWindows1251, charmap.Windows1251},
	{EncodingKOI8R, charmap.KOI8R},
	{EncodingKOI8U, charmap.KOI8U},
	{EncodingISO8859_5, charmap.ISO8859_5},
	{EncodingCP437, charmap.CodePage437},
	{EncodingCP850, charmap.CodePage850},
	{EncodingCP866, charmap.CodePage866},
}

// OutputEncodings returns a fresh, ordered list of supported output encodings.
func OutputEncodings() []OutputEncoding {
	result := make([]OutputEncoding, len(outputEncodings))
	for i, entry := range outputEncodings {
		result[i] = entry.id
	}
	return result
}

func (t *Terminal) OutputEncoding() OutputEncoding {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.outputEncoding
}

// SetOutputEncoding affects future output only; an unfinished character is
// displayed as a replacement character without clearing existing text.
func (t *Terminal) SetOutputEncoding(selected OutputEncoding) error {
	var codec encoding.Encoding
	found := false
	for _, entry := range outputEncodings {
		if entry.id == selected {
			codec, found = entry.codec, true
			break
		}
	}
	if !found {
		return fmt.Errorf("vt: unsupported output encoding %q", selected)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if selected == t.outputEncoding {
		return nil
	}
	t.flushOutputCharacter()
	t.outputEncoding = selected
	t.outputDecoder = nil
	if codec != nil {
		t.outputDecoder = codec.NewDecoder()
	}
	return nil
}

// ResetOutputStream starts a new program stream, retaining the screen and
// encoding selection. Unfinished characters become visible replacements;
// unfinished control sequences are discarded.
func (t *Terminal) ResetOutputStream() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushOutputCharacter()
	t.state = sGround
	t.osc = t.osc[:0]
	t.osc52, t.oscScan = false, osc52Scan{}
	t.onlyOSC = false
	t.resetDCS()
}

func (t *Terminal) flushOutputCharacter() {
	if len(t.utf8buf) != 0 || t.outputLen != 0 {
		t.print(utf8.RuneError)
		t.gen++
	}
	t.utf8buf = t.utf8buf[:0]
	t.outputLen = 0
}

// writeEncoded runs under mu. Only ground-state display text is transcoded.
func (t *Terminal) writeEncoded(p []byte) (int, error) {
	for i, b := range p {
		// In particular Big5 can consume DEL as an invalid trail. Never let a
		// decoder consume controls, even when it is waiting for more source.
		if b < 0x20 || b == 0x7f {
			t.flushOutputCharacter()
			t.byte(b)
			continue
		}
		if t.state != sGround || (t.outputLen == 0 && b < 0x80) {
			t.byte(b)
			continue
		}
		if t.outputLen == len(t.outputBuf) {
			return i, fmt.Errorf("vt: decode %s: character exceeds buffer", t.outputEncoding)
		}
		t.outputBuf[t.outputLen] = b
		t.outputLen++
		var decoded [16]byte // Four source bytes, including Big5's two-rune mappings.
		n, consumed, err := t.outputDecoder.Transform(decoded[:], t.outputBuf[:t.outputLen], false)
		for _, r := range string(decoded[:n]) {
			t.print(r)
		}
		t.outputLen = copy(t.outputBuf[:], t.outputBuf[consumed:t.outputLen])
		if err != nil && err != transform.ErrShortSrc {
			return i + 1, fmt.Errorf("vt: decode %s: %w", t.outputEncoding, err)
		}
	}
	return len(p), nil
}

// Decode is p, output in encoding e, as UTF-8 text; what does not decode
// becomes U+FFFD. An encoding not supported is taken as UTF-8.
func (e OutputEncoding) Decode(p []byte) string {
	for _, entry := range outputEncodings {
		if entry.id == e && entry.codec != nil {
			out, _, err := transform.Bytes(entry.codec.NewDecoder(), p)
			if err == nil {
				return string(out)
			}
		}
	}
	return string(p)
}
