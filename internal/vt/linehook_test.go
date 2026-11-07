package vt

import (
	"strings"
	"testing"
)

// hooked is a terminal whose line hook keeps what it was told, as a log
// would: wrapped lines joined, the others ended with a newline.
func hooked(cols, rows int) (*Terminal, *strings.Builder) {
	t := New(cols, rows)
	var b strings.Builder
	t.SetLineHook(func(text string, wrapped bool) {
		b.WriteString(text)
		if !wrapped {
			b.WriteByte('\n')
		}
	})
	return t, &b
}

func TestLineHookTellsLinesAsShown(t *testing.T) {
	term, got := hooked(20, 4)
	// Overwritten by a carriage return, rubbed out by backspaces, erased to
	// the end, colored: each line as it ended up.
	term.WriteString("50%\r100% done\r\n")
	term.WriteString("echo hx\b \bi\r\n")
	term.WriteString("stale text\r\x1b[Knew\r\n")
	term.WriteString("\x1b[31mred\x1b[0m plain   \r\n")
	// Enough lines to scroll: every one is still told, once.
	for _, s := range []string{"a", "b", "c", "d"} {
		term.WriteString(s + "\r\n")
	}
	want := "100% done\necho hi\nnew\nred plain\na\nb\nc\nd\n"
	if got.String() != want {
		t.Fatalf("got %q, want %q", got.String(), want)
	}
	term.WriteString("$ ")
	if got.String() != want || term.CursorLine() != "$" {
		t.Fatalf("the unfinished line was told: %q, cursor line %q", got.String(), term.CursorLine())
	}
}

func TestLineHookJoinsWrappedLines(t *testing.T) {
	term, got := hooked(5, 3)
	term.WriteString("abcdefghijkl\r\n")
	// A wide character that does not fit the last column moves on, leaving
	// a blank that is not part of the text.
	term.WriteString("abcd中文\r\n")
	if want := "abcdefghijkl\nabcd中文\n"; got.String() != want {
		t.Fatalf("got %q, want %q", got.String(), want)
	}
}

func TestLineHookSkipsAlternateScreen(t *testing.T) {
	term, got := hooked(20, 4)
	term.WriteString("before\r\n\x1b[?1049h")
	term.WriteString("vim line 1\r\nvim line 2\r\n")
	if term.CursorLine() != "" {
		t.Fatalf("cursor line on the alternate screen: %q", term.CursorLine())
	}
	term.WriteString("\x1b[?1049lafter\r\n")
	if want := "before\nafter\n"; got.String() != want {
		t.Fatalf("got %q, want %q", got.String(), want)
	}
}

func TestLineHookDecodedOutput(t *testing.T) {
	term, got := hooked(20, 4)
	if err := term.SetOutputEncoding(EncodingGBK); err != nil {
		t.Fatal(err)
	}
	term.Write([]byte{0xc4, 0xe3, 0xba, 0xc3, '\r', '\n'}) // 你好 in GBK
	if want := "你好\n"; got.String() != want {
		t.Fatalf("got %q, want %q", got.String(), want)
	}
}

func TestLineHookStops(t *testing.T) {
	term, got := hooked(20, 4)
	term.WriteString("one\r\n")
	term.SetLineHook(nil)
	term.WriteString("two\r\n")
	if got.String() != "one\n" {
		t.Fatalf("told after stopping: %q", got.String())
	}
}
