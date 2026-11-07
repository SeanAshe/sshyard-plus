package session

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/internal/shellhist"
)

func TestParseHistoryDump(t *testing.T) {
	mark := "@M"
	out := []byte("@M login /bin/zsh\n\n@M file zsh 30 /h/.zsh_history\n: 1700000000:0;ls\nno newline\n@M file bash 9999999 /h/.bash_history\npart\nmake\n\n")
	files, login := ParseHistoryDump(out, mark)
	if login != "/bin/zsh" || len(files) != 2 {
		t.Fatalf("login %q, files %d", login, len(files))
	}
	if string(files[0].Data) != ": 1700000000:0;ls\nno newline" || files[0].Shell != shellhist.Zsh || files[0].Cut {
		t.Fatalf("zsh file %+v", files[0])
	}
	if string(files[1].Data) != "part\nmake\n\n" || !files[1].Cut {
		t.Fatalf("bash file %+v", files[1])
	}
	// A line that only looks like a mark is the file's.
	files, _ = ParseHistoryDump([]byte("@M file sh 5 /h/.sh_history\n@Mx file\n@M bogus\n"), mark)
	if len(files) != 1 || string(files[0].Data) != "@Mx file\n" {
		t.Fatalf("fake mark %+v", files)
	}
}

func TestHistoryScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
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
	write(".zsh_history", ": 1700000000:0;git status\n: 1700000100:0;ls\n")
	write(".bash_history", "make\nls")
	write(".local/share/fish/fish_history", "- cmd: htop\n  when: 1700000050\n")
	if err := os.WriteFile(filepath.Join(home, ".sh_history"), []byte("x"), 0); err != nil {
		t.Fatal(err)
	}
	mark := historyMark()
	cmd := exec.Command("sh", "-s", "--", mark)
	cmd.Stdin = strings.NewReader(historyScript)
	cmd.Env = []string{"HOME=" + home, "SHELL=/bin/zsh", "PATH=" + os.Getenv("PATH")}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	files, login := ParseHistoryDump(out.Bytes(), mark)
	h := shellhist.Merge(files, 0)
	if login != "/bin/zsh" {
		t.Fatalf("login %q", login)
	}
	var got []string
	for _, e := range h.Entries {
		got = append(got, e.Cmd)
	}
	// The unreadable .sh_history is skipped, unless the test runs as root.
	if os.Geteuid() != 0 && h.Has(shellhist.Sh) {
		t.Fatal("unreadable file read")
	}
	if want := []string{"ls", "htop", "git status", "make"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if h.Entries[0].Count != 2 {
		t.Fatalf("ls count %d", h.Entries[0].Count)
	}
}
