package vt

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func setEncoding(t *testing.T, term *Terminal, encoding OutputEncoding) {
	t.Helper()
	if err := term.SetOutputEncoding(encoding); err != nil {
		t.Fatal(err)
	}
}

func TestGBKOutputPartitions(t *testing.T) {
	// 0x40 is an ASCII-range trail: it must join 0x81, not print '@'.
	data := []byte("A\xd6\xd0\xce\xc4\x81\x40Z")
	for mask := 0; mask < 1<<(len(data)-1); mask++ {
		term := New(20, 1)
		setEncoding(t, term, EncodingGBK)
		start := 0
		for i := 0; i < len(data); i++ {
			if i == len(data)-1 || mask&(1<<i) != 0 {
				if n, err := term.Write(data[start : i+1]); err != nil || n != i+1-start {
					t.Fatalf("partition %d: write %d, %v", mask, n, err)
				}
				start = i + 1
			}
		}
		if got := term.ScreenText(); got != "A中文丂Z" {
			t.Fatalf("partition %d: %q", mask, got)
		}
	}
}

func TestGBKInvalidAndControls(t *testing.T) {
	for _, tc := range []struct{ data, want string }{
		{"\xffX", "�X"}, {"\x81!X", "�!X"}, {"\xa1\x40X", "�X"},
		{"\x80", "€"}, {"\x81\x7fX", "�X"},
		{"\x81\x1b[31mX", "�X"}, {"\x81\r\nX", "�\nX"},
	} {
		t.Run(fmt.Sprintf("%x", tc.data), func(t *testing.T) {
			term := New(20, 2)
			setEncoding(t, term, EncodingGBK)
			for _, b := range []byte(tc.data) {
				term.Write([]byte{b})
			}
			if got := term.ScreenText(); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if strings.Contains(tc.data, "31m") && cell(term, 1, 0).FG != Indexed(1) {
				t.Fatal("ESC was swallowed")
			}
		})
	}
}

func TestOutputEncodingSwitch(t *testing.T) {
	term := New(40, 1)
	if term.OutputEncoding() != EncodingUTF8 {
		t.Fatal("default encoding is not UTF-8")
	}
	term.WriteString("中文\xe4")
	if err := term.SetOutputEncoding("invalid"); err == nil || term.OutputEncoding() != EncodingUTF8 {
		t.Fatal("unknown encoding accepted or changed selection")
	}
	if term.ScreenText() != "中文" {
		t.Fatal("unknown encoding flushed the pending character")
	}
	setEncoding(t, term, EncodingUTF8) // Same selection preserves the fragment.
	setEncoding(t, term, EncodingGBK)
	term.WriteString("\xd6\xd0\x81")
	setEncoding(t, term, EncodingGBK)
	setEncoding(t, term, EncodingUTF8)
	term.WriteString("文")
	if got := term.ScreenText(); got != "中文�中�文" {
		t.Fatalf("switch changed previous text or lost fragment: %q", got)
	}
	setEncoding(t, term, EncodingGBK)
	term.WriteString("\x1bc\xd6\xd0")
	if term.OutputEncoding() != EncodingGBK || term.ScreenText() != "中" {
		t.Fatal("terminal reset changed encoding selection")
	}
}

func TestGBKStyledText(t *testing.T) {
	term := New(20, 1)
	setEncoding(t, term, EncodingGBK)
	term.WriteString("\x1b[1;31m\xd6\xd0\xce\xc4\x1b[0m!")
	if c := cell(term, 0, 0); c.Rune != '中' || c.Width != 2 || c.FG != Indexed(1) || c.Attr != Bold {
		t.Fatalf("styled wide character: %+v", c)
	}
	if cell(term, 1, 0).Width != 0 || cell(term, 4, 0).Attr != 0 {
		t.Fatal("wide continuation or style reset incorrect")
	}
	if got := term.Text(Pos{0, 0}, Pos{0, 4}); got != "中文!" {
		t.Fatalf("copy text %q", got)
	}
	if matches := term.Find("文", false); len(matches) != 1 || matches[0].Col != 2 || matches[0].Len != 2 {
		t.Fatalf("find %+v", matches)
	}
}

func TestGBKProtocolsPreserved(t *testing.T) {
	term, got, drops := collect(t)
	setEncoding(t, term, EncodingGBK)
	data := "\x1b]7;file://host/home/中文\x07\x1b]2;标题\x07" +
		"\x1b]52;c;" + b64("中文📋") + "\x07" +
		tmuxWrap("\x1b]52;c;"+b64("tmux中文")+"\x07") +
		"\x1bPignored\xd6\xd0\x1b\\\xd6\xd0"
	for _, b := range []byte(data) {
		term.Write([]byte{b})
	}
	if term.Cwd() != "/home/中文" || term.Title() != "标题" {
		t.Fatalf("OSC changed: cwd %q title %q", term.Cwd(), term.Title())
	}
	if len(*got) != 2 || (*got)[0].data != "中文📋" || (*got)[1].data != "tmux中文" || *drops != 0 {
		t.Fatalf("OSC52 changed: %+v, drops %d", *got, *drops)
	}
	if term.ScreenText() != "中" {
		t.Fatalf("protocol leaked: %q", term.ScreenText())
	}
}

func TestResetOutputStream(t *testing.T) {
	for _, encoding := range []OutputEncoding{EncodingUTF8, EncodingGBK} {
		term := New(40, 1)
		setEncoding(t, term, encoding)
		term.WriteString("old\xe4")
		term.ResetOutputStream()
		term.WriteString("@")
		if term.OutputEncoding() != encoding || term.ScreenText() != "old�@" {
			t.Fatalf("stream boundary: encoding %s text %q", term.OutputEncoding(), term.ScreenText())
		}
		term.WriteString("\x1b]2;unfinished")
		term.ResetOutputStream()
		term.WriteString("new")
		if term.Title() != "" || term.ScreenText() != "old�@new" {
			t.Fatal("control sequence crossed stream boundary")
		}
	}
}

func TestOutputEncodingConcurrent(t *testing.T) {
	term := New(40, 2)
	var wg sync.WaitGroup
	for worker := 0; worker < 3; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				selected := OutputEncodings()[i%len(OutputEncodings())]
				if err := term.SetOutputEncoding(selected); err != nil {
					t.Error(err)
				}
				term.WriteString("\xd6\xd0")
				term.OutputEncoding()
				term.SetOutputEncoding(EncodingUTF8)
				term.WriteString("中")
				term.Snapshot(0)
				term.ResetOutputStream()
			}
		}()
	}
	wg.Wait()
}

// TestOutputEncodingDecode: bytes in an output encoding decode to text, and
// UTF-8 or an unknown encoding keeps them as they are.
func TestOutputEncodingDecode(t *testing.T) {
	if got := EncodingGBK.Decode([]byte{0xc4, 0xe3, 0xba, 0xc3, '#'}); got != "你好#" {
		t.Fatalf("GBK %q", got)
	}
	if got := EncodingUTF8.Decode([]byte("héllo")); got != "héllo" {
		t.Fatalf("UTF-8 %q", got)
	}
	if got := OutputEncoding("nope").Decode([]byte("abc")); got != "abc" {
		t.Fatalf("unknown %q", got)
	}
}
