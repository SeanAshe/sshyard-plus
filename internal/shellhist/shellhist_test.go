package shellhist

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func cmds(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Cmd)
	}
	return out
}

func TestParseBash(t *testing.T) {
	plain := Parse(Bash, []byte("ls -la\n\ncd /tmp\r\necho hi\n"), false)
	if got := cmds(plain); !slices.Equal(got, []string{"ls -la", "cd /tmp", "echo hi"}) {
		t.Fatalf("plain: %q", got)
	}
	for _, e := range plain {
		if !e.At.IsZero() || e.Shell != Bash || e.Count != 1 {
			t.Fatalf("plain entry %+v", e)
		}
	}
	timed := Parse(Bash, []byte("old\n#1700000000\nls\n#1700000060\npython train.py \\\n  --lr 1e-3\n#notatime\n"), false)
	if got := cmds(timed); !slices.Equal(got, []string{"old", "ls", "python train.py \\\n  --lr 1e-3\n#notatime"}) {
		t.Fatalf("timed: %q", got)
	}
	if !timed[0].At.IsZero() || timed[1].At.Unix() != 1700000000 || timed[2].At.Unix() != 1700000060 {
		t.Fatalf("times: %v %v %v", timed[0].At, timed[1].At, timed[2].At)
	}
}

func TestParseZsh(t *testing.T) {
	data := []byte(": 1700000000:0;git status\n: 1700000100:3;docker run \\\\\n  -it alpine\\\nsecond line\nplain one\n")
	es := Parse(Zsh, data, false)
	want := []string{"git status", "docker run \\\n  -it alpine\nsecond line", "plain one"}
	if got := cmds(es); !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if es[0].At.Unix() != 1700000000 || es[1].At.Unix() != 1700000100 || !es[2].At.IsZero() {
		t.Fatalf("times: %v", es)
	}
	// zsh metafies bytes such as 0x83..0x9f: 0x83 then the byte xor 32.
	meta := []byte{':', ' ', '1', '7', '0', '0', '0', '0', '0', '0', '0', '0', ':', '0', ';', 'e', 'c', 'h', 'o', ' ', 0xe4, 0xb8, 0x83, 0xad ^ 0x20, '\n'}
	if got := Parse(Zsh, meta, false); len(got) != 1 || got[0].Cmd != "echo 中" {
		t.Fatalf("metafied: %q", cmds(got))
	}
}

func TestParseFish(t *testing.T) {
	data := []byte("- cmd: echo a\\\\b\\nc\n  when: 1700000000\n  paths:\n    - /tmp\n- cmd: ls\n  when: 1700000005\n")
	es := Parse(Fish, data, false)
	if got := cmds(es); !slices.Equal(got, []string{"echo a\\b\nc", "ls"}) {
		t.Fatalf("got %q", got)
	}
	if es[0].At.Unix() != 1700000000 || es[1].At.Unix() != 1700000005 {
		t.Fatalf("times: %v", es)
	}
}

func TestParsePowerShellAndCut(t *testing.T) {
	es := Parse(PowerShell, []byte("Get-ChildItem `\n  -Recurse\r\ndir\r\n"), false)
	if got := cmds(es); !slices.Equal(got, []string{"Get-ChildItem \n  -Recurse", "dir"}) {
		t.Fatalf("pwsh: %q", got)
	}
	if got := cmds(Parse(Sh, []byte("rtial line\nls\n"), true)); !slices.Equal(got, []string{"ls"}) {
		t.Fatalf("cut: %q", got)
	}
	if got := Parse(Sh, []byte("no newline at all"), true); len(got) != 0 {
		t.Fatalf("cut single line: %q", cmds(got))
	}
}

func TestParseDropsControls(t *testing.T) {
	es := Parse(Sh, []byte("echo \x1b[31mred\x07\tok\n\x1b\n\xff\xfeabc\n"), false)
	if got := cmds(es); !slices.Equal(got, []string{"echo [31mred\tok", "\uFFFDabc"}) {
		t.Fatalf("got %q", got)
	}
}

func TestMerge(t *testing.T) {
	files := []File{
		{Shell: Zsh, Data: []byte(": 1700000000:0;ls\n: 1700000200:0;git pull\n: 1700000300:0;ls\n")},
		{Shell: Bash, Data: []byte("make\nls\n")},
		{Shell: Fish, Data: []byte("- cmd: htop\n  when: 1700000250\n")},
	}
	h := Merge(files, 0)
	want := []string{"ls", "htop", "git pull", "make"}
	if got := cmds(h.Entries); !slices.Equal(got, want) {
		t.Fatalf("order %q, want %q", got, want)
	}
	if e := h.Entries[0]; e.Count != 3 || e.Shell != Zsh || e.At.Unix() != 1700000300 {
		t.Fatalf("newest ls %+v", e)
	}
	if !slices.Equal(h.Shells, []string{Zsh, Bash, Fish}) || !h.Has(Fish) || h.Has(Sh) {
		t.Fatalf("shells %v", h.Shells)
	}
	if !h.Timed("") || !h.Timed(Zsh) || h.Timed(Bash) {
		t.Fatal("timed")
	}
	if got := Merge(files, 2); len(got.Entries) != 2 || got.Entries[0].Count != 3 {
		t.Fatalf("max: %+v", got.Entries)
	}
	// An untimed file keeps its own order, the later line first.
	if got := cmds(Merge([]File{{Shell: Bash, Data: []byte("a\nb\nc\nb\n")}}, 0).Entries); !slices.Equal(got, []string{"b", "c", "a"}) {
		t.Fatalf("untimed: %q", got)
	}
}

func TestReadLocal(t *testing.T) {
	home := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".zsh_history", ": 1700000000:0;zsh one\n")
	write(".local/share/fish/fish_history", "- cmd: fish one\n  when: 1700000001\n")
	big := strings.Repeat("x", TailBytes) + "\nlast\n"
	write(".bash_history", big)
	env := map[string]string{}
	h := ReadLocal(LocalCandidates("linux", home, func(k string) string { return env[k] }), LoginShell("/usr/bin/zsh"))
	if got := cmds(h.Entries); !slices.Equal(got, []string{"fish one", "zsh one", "last"}) {
		t.Fatalf("got %q", got)
	}
	if h.Login != Zsh {
		t.Fatalf("login %q", h.Login)
	}
	win := LocalCandidates("windows", home, func(k string) string { return map[string]string{"APPDATA": `C:\Users\u\AppData\Roaming`}[k] })
	if len(win) != 1 || win[0].Shell != PowerShell || !strings.HasSuffix(win[0].Path, "ConsoleHost_history.txt") {
		t.Fatalf("windows %v", win)
	}
}

func TestLoginShell(t *testing.T) {
	for in, want := range map[string]string{"/bin/bash": Bash, "/usr/local/bin/fish": Fish, `C:\Program Files\PowerShell\7\pwsh.exe`: PowerShell, "/bin/dash": Sh, "/usr/bin/nu": "", "": ""} {
		if got := LoginShell(in); got != want {
			t.Errorf("LoginShell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnixTime(t *testing.T) {
	if _, ok := unixTime("12"); ok {
		t.Fatal("small number taken as a time")
	}
	if at, ok := unixTime("1700000000"); !ok || !at.Equal(time.Unix(1700000000, 0)) {
		t.Fatal("time")
	}
}
