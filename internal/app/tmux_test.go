package app

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/locales"
	"golang.org/x/crypto/ssh"
)

// tmuxExec plays a server with tmux: the session dev ($1) with two
// windows, build ($2) when two is set, and the client /dev/pts/7 on dev,
// the asking terminal's when mine is set. It keeps the commands it was
// asked to run.
type tmuxExec struct {
	mu       sync.Mutex
	cmds     []string
	missing  bool
	noServer bool
	two      bool
	mine     bool
	shell    string // what the idle script says: idle, busy, or "" for unknown
	// made is set once a session was created, then listed as $9
	made bool
}

func (x *tmuxExec) run(ch ssh.Channel, cmd string) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.missing && strings.Contains(cmd, "command -v tmux") {
		return 127
	}
	switch {
	case strings.Contains(cmd, "list-sessions"):
		io.WriteString(ch, "@V tmux 3.4\n")
		if x.noServer {
			return 0
		}
		io.WriteString(ch, "@S $1\t2\t1\t1700000000\t1700000000\t/srv/api\tdev\n")
		if x.two {
			io.WriteString(ch, "@S $2\t1\t0\t1700000000\t1700000000\t\tbuild\n")
		}
		if x.made {
			io.WriteString(ch, "@S $9\t1\t0\t1700000000\t1700000000\t\tnew\n")
		}
		io.WriteString(ch, "@W $1\t0\t0\t1\tzsh\n@W $1\t1\t1\t2\tvim\n")
		mine := "0"
		if x.mine && !strings.HasSuffix(cmd, " _ ''") {
			mine = "1"
		}
		io.WriteString(ch, "@C /dev/pts/7\t$1\t"+mine+"\n")
	case strings.Contains(cmd, "grep -lzx"):
		if x.shell == "" {
			return 1
		}
		io.WriteString(ch, x.shell+"\n")
	case strings.Contains(cmd, "detach-client"):
		x.cmds = append(x.cmds, cmd)
		x.mine = false
	case strings.Contains(cmd, "new-session -d"):
		x.cmds = append(x.cmds, cmd)
		x.made = true
		io.WriteString(ch, "$9\n")
	default:
		x.cmds = append(x.cmds, cmd)
	}
	return 0
}

// did reports whether a command running op ran with args.
func (x *tmuxExec) did(op string, args ...string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	tail := " _"
	for _, a := range args {
		tail += " '" + a + "'"
	}
	for _, c := range x.cmds {
		if strings.Contains(c, op) && strings.HasSuffix(c, tail) {
			return true
		}
	}
	return false
}

// TestTmuxPanel: the tmux panel lists the server's sessions and their
// windows, attaches the terminal to a window, renames, ends and creates
// sessions.
func TestTmuxPanel(t *testing.T) {
	x := &tmuxExec{shell: "idle"}
	srv := startMetricSrv(t, x.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	m := locales.In("en")
	cn := connected(t, a, tt, srv)
	mustClick(t, tt, "tmux Sessions")
	tt.Frame()
	if !a.toolHere(layout.Tmux) {
		t.Fatal("the pane did not open")
	}
	waitFor(t, tt, "the sessions", func() bool { return tt.HasText("dev") })
	for _, s := range []string{"tmux 3.4 · 1 session", "2 windows · 1 client", "/srv/api"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q: %q", s, tt.Texts())
		}
	}
	if tt.HasText("1: vim") || tt.HasText("This terminal") {
		t.Errorf("windows or the terminal's chip show: %q", tt.Texts())
	}

	mustClick(t, tt, "Show Windows")
	tt.Frame()
	if !tt.HasText("1: vim") || !tt.HasText("2 panes") {
		t.Fatalf("windows: %q", tt.Texts())
	}
	mustClick(t, tt, "Attach to Window 1")
	waitFor(t, tt, "the window", func() bool { return x.did("select-window", "$1", "1") })
	waitFor(t, tt, "the attach command", func() bool {
		return strings.Contains(cn.s.Term.ScreenText(), "tmux attach-session -t '$1'")
	})
	if k := markOfConn(cn); k == nil || k.id != "$1" || k.name != "dev" {
		t.Errorf("the session attached to: %+v", k)
	}

	// A name tmux would change is refused in the sheet, which stays.
	mustClick(t, tt, "Rename Session")
	tt.Frame()
	if !a.tmux.formOpen || a.tmux.form.name != "dev" {
		t.Fatalf("rename sheet %v %+v", a.tmux.formOpen, a.tmux.form)
	}
	a.tmux.form.name = "a:b"
	a.submitTmuxForm(m, cn)
	tt.Frame()
	if !a.tmux.formOpen || !tt.HasText(m.TmuxNameInvalid()) {
		t.Fatalf("a bad name: %v %q", a.tmux.formOpen, tt.Texts())
	}
	a.tmux.form.name = " api "
	a.submitTmuxForm(m, cn)
	waitFor(t, tt, "rename", func() bool { return x.did("rename-session", "$1", "api") })
	waitFor(t, tt, "done", func() bool { return a.tmux.busy == "" })

	mustClick(t, tt, "End Session")
	tt.Frame()
	if !tt.HasText(m.TmuxKillConfirmAttached("dev")) {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	mustClick(t, tt, "End")
	waitFor(t, tt, "kill", func() bool { return x.did("kill-session", "$1") })
	waitFor(t, tt, "done", func() bool { return a.tmux.busy == "" })

	// A new session is named so as not to clash, and created in the
	// background or opened in a new tab.
	mustClick(t, tt, "New tmux Session")
	tt.Frame()
	if f := a.tmux.form; !a.tmux.formOpen || f.id != "" || f.name != "main" || f.where != tmuxOpenHere {
		t.Fatalf("new sheet %v %+v", a.tmux.formOpen, f)
	}
	a.tmux.form.name, a.tmux.form.dir, a.tmux.form.where = "dev", "", tmuxOpenNone
	a.submitTmuxForm(m, cn)
	if a.tmux.form.err != m.TmuxNameTaken("dev") {
		t.Fatalf("a taken name: %q", a.tmux.form.err)
	}
	a.tmux.form.name, a.tmux.form.dir = "work", "~/src"
	a.submitTmuxForm(m, cn)
	waitFor(t, tt, "new", func() bool { return x.did("new-session -d", "work", "~/src") })
	waitFor(t, tt, "done", func() bool { return a.tmux.busy == "" })

	tabs := len(a.ws.Tabs)
	a.openTmuxForm(cn, nil)
	a.tmux.form.name, a.tmux.form.where = "web", tmuxOpenTab
	a.submitTmuxForm(m, cn)
	waitFor(t, tt, "the tab", func() bool { return len(a.ws.Tabs) == tabs+1 })
	nc := a.focused()
	waitFor(t, tt, "attached in the tab", func() bool {
		return strings.Contains(nc.s.Term.ScreenText(), "tmux attach-session -t '$9'")
	})
}

// TestTmuxPanelMine: the terminal attached to a session says so, detaches
// from it, and switches to another session without typing.
func TestTmuxPanelMine(t *testing.T) {
	x := &tmuxExec{mine: true, two: true}
	srv := startMetricSrv(t, x.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 900)
	cn := connected(t, a, tt, srv)
	a.ShowTool(layout.Tmux)
	tt.Frame()
	waitFor(t, tt, "the banner", func() bool { return tt.HasText("This terminal is attached to “dev”.") })
	if !tt.HasText("This terminal") || !tt.HasText("build") || !tt.HasText("Not attached") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if k := markOfConn(cn); k == nil || k.id != "$1" {
		t.Errorf("the session the terminal is in: %+v", k)
	}
	mustClick(t, tt, "Detach from Session")
	waitFor(t, tt, "detach", func() bool { return x.did("detach-client", "/dev/pts/7") })
	waitFor(t, tt, "done", func() bool { return a.tmux.busy == "" })
	if k := markOfConn(cn); k != nil {
		t.Errorf("kept after detaching: %+v", k)
	}

	// Attached again, it switches to another session without typing.
	x.mu.Lock()
	x.mine = true
	x.mu.Unlock()
	a.tmux.at = time.Time{}
	waitFor(t, tt, "the banner again", func() bool { return tt.HasText("This terminal is attached to “dev”.") })

	a.tmuxAttach(cn, "$2", -1)
	waitFor(t, tt, "switch", func() bool { return x.did("switch-client", "/dev/pts/7", "$2") })
	if strings.Contains(cn.s.Term.ScreenText(), "tmux attach-session") {
		t.Error("the attach command was typed into a terminal in tmux")
	}
}

// TestTmuxAttachBusy: a terminal running a program, or of which the
// server cannot tell, attaches in a new tab instead, saying why.
func TestTmuxAttachBusy(t *testing.T) {
	for _, shell := range []string{"busy", ""} {
		x := &tmuxExec{shell: shell}
		srv := startMetricSrv(t, x.run)
		a, tt := newTestApp(t)
		tt.SetSize(1400, 900)
		m := locales.In("en")
		cn := connected(t, a, tt, srv)
		a.ShowTool(layout.Tmux)
		tt.Frame()
		waitFor(t, tt, "the sessions", func() bool { return tt.HasText("dev") })
		tabs := len(a.ws.Tabs)
		a.tmuxAttach(cn, "$1", -1)
		waitFor(t, tt, "the tab", func() bool { return len(a.ws.Tabs) == tabs+1 })
		want := m.TmuxBusyNewTab()
		if shell == "" {
			want = m.TmuxUnknownNewTab()
		}
		waitFor(t, tt, "the toast", func() bool { return tt.HasText(want) })
		if strings.Contains(cn.s.Term.ScreenText(), "tmux attach-session") {
			t.Errorf("%q: typed into the terminal", shell)
		}
	}
}

// TestTmuxPanelEmpty: a server without tmux, or without sessions, says so.
func TestTmuxPanelEmpty(t *testing.T) {
	for x, want := range map[*tmuxExec][]string{
		{missing: true}:  {"tmux is not installed on this server."},
		{noServer: true}: {"No tmux sessions on this server yet.", "tmux 3.4 · 0 sessions"},
	} {
		srv := startMetricSrv(t, x.run)
		a, tt := newTestApp(t)
		connected(t, a, tt, srv)
		a.ShowTool(layout.Tmux)
		tt.Frame()
		for _, s := range want {
			waitFor(t, tt, s, func() bool { return tt.HasText(s) })
		}
	}
}

// TestTmuxFreeName: a new session's name is the first not taken.
func TestTmuxFreeName(t *testing.T) {
	ss := []session.TmuxSession{{Name: "main"}, {Name: "main-2"}, {Name: "dev"}}
	if got := tmuxFreeName(ss); got != "main-3" {
		t.Errorf("got %q", got)
	}
	if got := tmuxFreeName(nil); got != "main" {
		t.Errorf("got %q", got)
	}
}
