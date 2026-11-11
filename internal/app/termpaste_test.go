package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/vt"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// formatClipboard is a clipboard that lists its formats, as ui.Services
// does.
type formatClipboard struct {
	text    string
	formats []transfer.Format
}

func (f formatClipboard) ReadClipboard() string               { return f.text }
func (f formatClipboard) WriteClipboard(string)               {}
func (f formatClipboard) ClipboardFormats() []transfer.Format { return f.formats }

// The window's clipboard lists its formats, as clipboardHasImage asks.
var _ interface{ ClipboardFormats() []transfer.Format } = ui.Services{}

// TestPasteClipboardImage: pasting text sends it as pasted; a clipboard
// with an image and no text sends Ctrl+V to a local terminal alone, for
// its program to read the image itself, and nothing to a remote one or
// to the terminals typed into with it.
func TestPasteClipboardImage(t *testing.T) {
	img := []transfer.Format{transfer.PNG}
	cases := []struct {
		name  string
		local bool
		cb    clipboard
		want  string
	}{
		{"local text", true, formatClipboard{text: "ls", formats: []transfer.Format{transfer.Text}}, "ls"},
		{"local text and image", true, formatClipboard{text: "ls", formats: append([]transfer.Format{transfer.Text}, img...)}, "ls"},
		{"local image", true, formatClipboard{formats: img}, "\x16"},
		{"remote image", false, formatClipboard{formats: img}, ""},
		{"remote text", false, formatClipboard{text: "ls", formats: []transfer.Format{transfer.Text}}, "ls"},
		{"local empty", true, formatClipboard{}, ""},
		{"local without formats", true, fakeClipboard{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out []byte
			tv := newTermView(recordTarget{term: vt.New(20, 4), out: &out}, 13)
			tv.local = tc.local
			var fanned []byte
			tv.fanout = func(encode func(vt.Modes) []byte) { fanned = append(fanned, encode(vt.Modes{})...) }
			if !tv.input(ui.InputEvent{Kind: ui.InputCommand, Text: "paste"}, tc.cb) {
				t.Fatal("paste not taken")
			}
			if string(out) != tc.want {
				t.Errorf("sent %q, want %q", out, tc.want)
			}
			if tc.want == "\x16" && len(fanned) != 0 {
				t.Errorf("Ctrl+V went to the other terminals too: %q", fanned)
			}
		})
	}
}
