package app

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/shellhist"
	"github.com/Licoy/sshyard/locales"
	"golang.org/x/crypto/ssh"
)

func TestHistoryFilter(t *testing.T) {
	es := []shellhist.Entry{
		{Cmd: "git push origin main", Shell: shellhist.Zsh},
		{Cmd: "Docker ps -a", Shell: shellhist.Bash},
		{Cmd: "git status", Shell: shellhist.Bash},
	}
	pick := func(shell, q string) []string {
		var out []string
		for _, e := range historyFilter(es, shell, q) {
			out = append(out, e.Cmd)
		}
		return out
	}
	if got := pick("", ""); len(got) != 3 {
		t.Fatalf("all: %q", got)
	}
	if got := pick("", "GIT  main"); !slices.Equal(got, []string{"git push origin main"}) {
		t.Fatalf("words: %q", got)
	}
	if got := pick(shellhist.Bash, "docker"); !slices.Equal(got, []string{"Docker ps -a"}) {
		t.Fatalf("shell and case: %q", got)
	}
	if got := pick(shellhist.Fish, ""); len(got) != 0 {
		t.Fatalf("other shell: %q", got)
	}
}

func TestHistoryDayAndName(t *testing.T) {
	m := locales.In("en")
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.Local)
	for at, want := range map[time.Time]string{
		{}: m.HistoryUntimed(),
		time.Date(2026, 11, 7, 0, 1, 0, 0, time.Local):   m.HistoryToday(),
		time.Date(2026, 11, 6, 23, 59, 0, 0, time.Local): m.HistoryYesterday(),
		time.Date(2026, 10, 1, 8, 0, 0, 0, time.Local):   "2026-10-01",
	} {
		if got := historyDay(m, at, now); got != want {
			t.Errorf("historyDay(%v) = %q, want %q", at, got, want)
		}
	}
	if got := historySnippetName("  python  train.py\t--lr 1 \\\n  --epochs 3"); got != "python train.py --lr 1 \\" {
		t.Fatalf("name %q", got)
	}
	long := historySnippetName("echo " + strings.Repeat("长", 60))
	if r := []rune(long); len(r) != 40 || r[39] != '…' {
		t.Fatalf("long name %q", long)
	}
}

func TestHistoryAddSnippet(t *testing.T) {
	a, _ := newTestApp(t)
	m := locales.In("en")
	cmd := "python train.py \\\n  --lr 1e-3"
	a.historySnippet(m, cmd)
	f := a.snippets.form
	if f == nil || !a.snippets.showing || f.content != cmd || f.meta.ID != "" || f.meta.Name != "python train.py \\" {
		t.Fatalf("form %+v", f)
	}
	if !a.saveSnippetForm(m) {
		t.Fatal(f.err)
	}
	body, err := a.snippetContent(a.snippets.selected)
	if err != nil || body != cmd {
		t.Fatalf("saved body %q, %v", body, err)
	}
}

func TestHistorySendToTerminal(t *testing.T) {
	srv, shell := startScript(t, "\x1b[?2004hready")
	a, tt := newTestApp(t)
	m := locales.In("en")
	// Without a terminal nothing is written, and the failure shows.
	a.runHistory(m, "uptime", true)
	if len(a.toasts) == 0 {
		t.Fatal("no terminal: no toast")
	}
	c := connected(t, a, tt, srv)
	waitFor(t, tt, "bracketed paste", func() bool { return c.s.Term.Modes().BracketedPaste })
	shell.take()
	cmd := "python run.py \\\n  --fast"
	want, err := snippetBytes(cmd, true, c.s.Term.Modes())
	if err != nil {
		t.Fatal(err)
	}
	a.runHistory(m, cmd, true)
	var got string
	waitFor(t, tt, "history bytes", func() bool { got += shell.take(); return len(got) >= len(want) })
	if got != string(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	waitFor(t, tt, "write done", func() bool { return !c.tv.snippetSending })
	// Inserting never sends Enter.
	want, _ = snippetBytes("ls", false, c.s.Term.Modes())
	a.runHistory(m, "ls", false)
	got = ""
	waitFor(t, tt, "insert bytes", func() bool { got += shell.take(); return len(got) >= len(want) })
	if got != string(want) {
		t.Fatalf("insert got %q, want %q", got, want)
	}
}

func TestHistoryReadGeneration(t *testing.T) {
	a, _ := newTestApp(t)
	a.hist = &historyState{gen: 2, running: true, shell: shellhist.Fish, limit: historyPage}
	old := shellhist.History{Entries: []shellhist.Entry{{Cmd: "old"}}, Shells: []string{shellhist.Fish}}
	a.historyRead(1, old, nil)
	if a.hist.read || !a.hist.running {
		t.Fatal("a read of another host was taken")
	}
	a.historyRead(2, shellhist.History{}, errors.New("denied"))
	if a.hist.read || a.hist.running || a.hist.err == nil {
		t.Fatalf("failed read %+v", a.hist)
	}
	h := shellhist.History{Entries: []shellhist.Entry{{Cmd: "ls", Shell: shellhist.Zsh}}, Shells: []string{shellhist.Zsh}}
	a.historyRead(2, h, nil)
	if !a.hist.read || a.hist.err != nil || len(a.hist.hist.Entries) != 1 || a.hist.shell != "" {
		t.Fatalf("read %+v", a.hist)
	}
}

func TestHistoryTipShows(t *testing.T) {
	bash := shellhist.Entry{Cmd: "ls", Shell: shellhist.Bash}
	timed := shellhist.Entry{Cmd: "ls", Shell: shellhist.Bash, At: time.Unix(1700000000, 0)}
	for _, tc := range []struct {
		name string
		h    shellhist.History
		want bool
	}{
		{"bash untimed", shellhist.History{Entries: []shellhist.Entry{bash}, Shells: []string{shellhist.Bash}, Login: shellhist.Bash}, true},
		{"unknown login, bash alone", shellhist.History{Entries: []shellhist.Entry{bash}, Shells: []string{shellhist.Bash}}, true},
		{"bash timed", shellhist.History{Entries: []shellhist.Entry{timed}, Shells: []string{shellhist.Bash}, Login: shellhist.Bash}, false},
		{"zsh user, old bash file", shellhist.History{Entries: []shellhist.Entry{bash}, Shells: []string{shellhist.Zsh, shellhist.Bash}, Login: shellhist.Zsh}, false},
		{"unknown login, several shells", shellhist.History{Entries: []shellhist.Entry{bash}, Shells: []string{shellhist.Zsh, shellhist.Bash}}, false},
	} {
		if got := historyTipShows(tc.h); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHistoryShortcut(t *testing.T) {
	a, _ := newTestApp(t)
	a.runAction(actHistory)
	if !a.panelOpen(inspectHistory) {
		t.Fatal("shortcut did not open the panel")
	}
	a.runAction(actHistory)
	if a.panelOpen(inspectHistory) {
		t.Fatal("shortcut did not close the panel")
	}
}

// homeExec runs each exec request with /bin/sh under home, as a server's
// user whose home folder it is.
func homeExec(home string) func(ch ssh.Channel, cmd string) int {
	return func(ch ssh.Channel, cmd string) int {
		c := exec.Command("/bin/sh", "-c", cmd)
		c.Env = []string{"HOME=" + home, "SHELL=/bin/bash", "PATH=" + os.Getenv("PATH")}
		c.Stdout, c.Stderr = ch, ch.Stderr()
		in, err := c.StdinPipe()
		if err != nil {
			return 127
		}
		go func() {
			io.Copy(in, ch)
			in.Close()
		}()
		if err := c.Run(); err != nil {
			return 1
		}
		return 0
	}
}

func TestHistoryPanelReadsServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".bash_history"), []byte("make\nsudo systemctl restart nginx\nmake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _ := startScript(t, "ready")
	srv.Exec = homeExec(home)
	a, tt := newTestApp(t)
	c := connected(t, a, tt, srv)
	a.openPanel(inspectHistory)
	tt.Frame()
	if a.hist == nil || a.hist.host != c.host.ID {
		t.Fatalf("panel did not read the focused host: %+v", a.hist)
	}
	waitFor(t, tt, "history read", func() bool { return a.hist.read || a.hist.err != nil })
	if a.hist.err != nil {
		t.Fatal(a.hist.err)
	}
	h := a.hist.hist
	if len(h.Entries) != 2 || h.Entries[0].Cmd != "make" || h.Entries[0].Count != 2 || h.Login != shellhist.Bash {
		t.Fatalf("history %+v", h)
	}
}
