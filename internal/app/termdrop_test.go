package app

import (
	"testing"

	"github.com/Licoy/sshyard/internal/vt"
)

func TestDropText(t *testing.T) {
	for _, tc := range []struct {
		name, goos, shell string
		paths             []string
		want              string
	}{
		{"plain", "darwin", "/bin/zsh", []string{"/Users/me/a.txt"}, "/Users/me/a.txt "},
		{"several", "linux", "/bin/bash", []string{"/a", "/b/c"}, "/a /b/c "},
		{"space and quote", "darwin", "/bin/zsh", []string{"/Users/me/My File's.txt"}, `/Users/me/My\ File\'s.txt `},
		{"shell specials", "linux", "/usr/bin/fish", []string{`/t/$x;(a)&b|c*?[d]~!#=%^"\e`},
			`/t/\$x\;\(a\)\&b\|c\*\?\[d\]\~\!\#\=\%\^\"\\e `},
		{"utf-8 kept", "darwin", "/bin/zsh", []string{"/文件/报告 1.pdf"}, `/文件/报告\ 1.pdf `},
		{"control char left out", "linux", "/bin/sh", []string{"/a\nrm -rf ~", "/b"}, "/b "},
		{"none left", "linux", "/bin/sh", []string{"/a\r"}, ""},
		{"empty", "darwin", "/bin/zsh", nil, ""},

		{"cmd plain", "windows", "cmd", []string{`C:\a\b.txt`}, `C:\a\b.txt `},
		{"cmd space", "windows", "cmd", []string{`C:\My Docs\b.txt`, `D:\x&y`}, `"C:\My Docs\b.txt" "D:\x&y" `},
		{"unknown windows shell as cmd", "windows", "", []string{`C:\a b`}, `"C:\a b" `},
		{"pwsh plain", "windows", "pwsh", []string{`C:\a\b.txt`}, `C:\a\b.txt `},
		{"pwsh space", "windows", "powershell", []string{`C:\My Docs\b.txt`}, `"C:\My Docs\b.txt" `},
		{"pwsh specials", "windows", "pwsh", []string{"C:\\a $b`c\u201dd"}, "\"C:\\a `$b``c`\u201dd\" "},
		{"pwsh single quote", "windows", "pwsh", []string{`C:\it's`}, `"C:\it's" `},

		{"git bash", "windows", "gitbash", []string{`C:\My Docs\b.txt`}, `/c/My\ Docs/b.txt `},
		{"git bash unc", "windows", "gitbash", []string{`\\srv\share\f`}, `//srv/share/f `},
		{"wsl drive", "windows", "wsl:Ubuntu", []string{`D:\a b\c`}, `/mnt/d/a\ b/c `},
		{"wsl own files", "windows", "wsl:Ubuntu", []string{`\\wsl.localhost\Ubuntu\home\me\f`, `\\wsl$\ubuntu\tmp`}, "/home/me/f /tmp "},
		{"wsl other distro", "windows", "wsl:Ubuntu", []string{`\\wsl$\Debian\f`}, `//wsl\$/Debian/f `},
	} {
		if got := dropText(tc.paths, tc.goos, tc.shell); got != tc.want {
			t.Errorf("%s: dropText(%q) = %q, want %q", tc.name, tc.paths, got, tc.want)
		}
	}
}

// TestDropTextPasted checks what the terminal gets: a paste, bracketed
// when the program asked, and never an Enter.
func TestDropTextPasted(t *testing.T) {
	text := dropText([]string{"/a b", "/c"}, "darwin", "/bin/zsh")
	if got := string(pasteBytes(text, vt.Modes{})); got != `/a\ b /c ` {
		t.Errorf("plain = %q", got)
	}
	if got := string(pasteBytes(text, vt.Modes{BracketedPaste: true})); got != "\x1b[200~/a\\ b /c \x1b[201~" {
		t.Errorf("bracketed = %q", got)
	}
}
