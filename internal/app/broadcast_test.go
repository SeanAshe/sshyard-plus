package app

import (
	"errors"
	"strconv"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/egoist/mygo/ui"
)

// broadcastPair connects two terminals side by side in a tab, the first
// in bracketed paste mode, and a third alone in a second tab, which is
// active.
func broadcastPair(t *testing.T) (*App, *ui.Tester, [3]*conn, [3]*scriptShell) {
	t.Helper()
	a, tt := newTestApp(t)
	var cs [3]*conn
	var shells [3]*scriptShell
	for i, first := range []string{"\x1b[?2004hready", "ready", "ready"} {
		srv, sh := startScript(t, first)
		h := addHost(t, a, srv, string(rune('a'+i)), "secret")
		a.known.Add(srv.Addr, srv.HostKey.PublicKey())
		at := openAt{}
		if i == 1 {
			at = openAt{pane: a.ws.ActiveTab().Focus, edge: layout.Right}
		}
		if _, err := a.Connect(h.ID, at); err != nil {
			t.Fatal(err)
		}
		cs[i], shells[i] = a.focused(), sh
	}
	for _, c := range cs {
		waitFor(t, tt, "connected", func() bool { return c.s.Status().State == session.Connected })
	}
	waitFor(t, tt, "bracketed paste", func() bool { return cs[0].s.Term.Modes().BracketedPaste })
	for _, sh := range shells {
		sh.take()
	}
	return a, tt, cs, shells
}

// readExactly waits for a shell to read want, and nothing else.
func readExactly(t *testing.T, tt *ui.Tester, sh *scriptShell, want string) {
	t.Helper()
	var got string
	waitFor(t, tt, "the input "+want, func() bool {
		got += sh.take()
		return len(got) >= len(want)
	})
	if got != want {
		t.Fatalf("read %q, want %q", got, want)
	}
}

// waitBroadcast runs frames until the writes of a send are done.
func waitBroadcast(t *testing.T, a *App, tt *ui.Tester) {
	t.Helper()
	waitFor(t, tt, "broadcast writes", func() bool { return a.bc.sending == 0 })
}

func TestBroadcastTargets(t *testing.T) {
	a, _, cs, _ := broadcastPair(t)
	ids := func(cs []*conn) []int {
		var out []int
		for _, c := range cs {
			out = append(out, c.id)
		}
		return out
	}
	// The active tab's terminals, then every tab's.
	if got := ids(a.broadcastTargets()); len(got) != 1 || got[0] != cs[2].id {
		t.Fatalf("active tab targets %v", got)
	}
	a.ws.Activate(cs[0].id)
	if got := ids(a.broadcastTargets()); len(got) != 2 || got[0] != cs[0].id || got[1] != cs[1].id {
		t.Fatalf("first tab targets %v", got)
	}
	a.bc.all = true
	a.bc.left = map[int]bool{cs[1].id: true}
	if got := ids(a.broadcastTargets()); len(got) != 2 || got[0] != cs[0].id || got[1] != cs[2].id {
		t.Fatalf("all tabs less one %v", got)
	}
	if len(a.broadcastPanes()) != 3 {
		t.Fatal("a pane left out is no longer listed")
	}
	// Files and AI panes are never targets.
	a.bc.left = nil
	cs[1].kind = layout.Files
	if got := ids(a.broadcastTargets()); len(got) != 2 {
		t.Fatalf("files pane among the targets: %v", got)
	}
	cs[1].kind = layout.Terminal
}

func TestBroadcastEncodesForEachTerminal(t *testing.T) {
	a, tt, cs, shells := broadcastPair(t)
	a.ws.Activate(cs[0].id)
	cs[1].tv.offset, cs[1].tv.selected = 4, true
	if err := a.sendBroadcast("echo 中文", true); err != nil {
		t.Fatal(err)
	}
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[0], "\x1b[200~echo 中文\x1b[201~\r")
	readExactly(t, tt, shells[1], "echo 中文\r")
	if got := shells[2].take(); got != "" {
		t.Fatalf("a terminal of another tab got %q", got)
	}
	if a.bc.sent[cs[0].id] != nil || a.bc.sent[cs[1].id] != nil || len(a.bc.sent) != 2 {
		t.Fatalf("results %v", a.bc.sent)
	}
	if cs[1].tv.offset != 0 || cs[1].tv.selected {
		t.Fatal("a terminal sent to kept its scroll and selection")
	}

	// Inserted, not run: a tab is only safe in bracketed paste. The
	// terminal unable to take it is skipped; the other still gets it.
	if err := a.sendBroadcast("a\tb", false); err != nil {
		t.Fatal(err)
	}
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[0], "\x1b[200~a\tb\x1b[201~")
	if !errors.Is(a.bc.sent[cs[1].id], errSnippetUnsafePaste) {
		t.Fatalf("unsafe insert result %v", a.bc.sent[cs[1].id])
	}
	if got := shells[1].take(); got != "" {
		t.Fatalf("unsafe insert sent %q", got)
	}
}

func TestBroadcastRefusals(t *testing.T) {
	a, tt, cs, shells := broadcastPair(t)
	a.ws.Activate(cs[0].id)
	var field *assets.FieldError
	if err := a.sendBroadcast("  ", true); !errors.As(err, &field) {
		t.Fatalf("empty command: %v", err)
	}
	if err := a.sendBroadcast("echo\x03", true); !errors.As(err, &field) {
		t.Fatalf("control character: %v", err)
	}
	a.bc.sending = 1
	if err := a.sendBroadcast("echo", true); !errors.Is(err, errSnippetBusy) {
		t.Fatalf("send while sending: %v", err)
	}
	a.bc.sending = 0
	a.bc.left = map[int]bool{cs[0].id: true, cs[1].id: true}
	if err := a.sendBroadcast("echo", true); !errors.Is(err, errBroadcastNoTarget) {
		t.Fatalf("send with every terminal left out: %v", err)
	}
	a.bc.left = nil

	// A terminal disconnected is skipped and said so.
	cs[1].s.Close()
	waitFor(t, tt, "closed", func() bool { return cs[1].s.Status().State != session.Connected })
	if err := a.sendBroadcast("uptime", true); err != nil {
		t.Fatal(err)
	}
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[0], "\x1b[200~uptime\x1b[201~\r")
	if !errors.Is(a.bc.sent[cs[1].id], errSnippetDisconnected) {
		t.Fatalf("disconnected result %v", a.bc.sent[cs[1].id])
	}
}

func TestBroadcastAsksBeforeSeveralLines(t *testing.T) {
	a, tt, cs, shells := broadcastPair(t)
	a.ws.Activate(cs[0].id)
	a.bc.draft = "cd /tmp\nls\n"
	a.submitBroadcast()
	if !a.bc.confirm || a.bc.sent != nil || a.bc.draft == "" {
		t.Fatalf("several lines sent without asking: confirm %v, sent %v, draft %q", a.bc.confirm, a.bc.sent, a.bc.draft)
	}
	a.bc.confirm = false
	a.confirmBroadcast()
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[1], "cd /tmp\rls\r")
	if a.bc.draft != "" || len(a.bc.history) != 1 || a.bc.history[0] != "cd /tmp\nls\n" {
		t.Fatalf("sent, draft %q, history %q", a.bc.draft, a.bc.history)
	}

	// One line, its line end aside, goes at once.
	a.bc.draft = "pwd\r\n"
	a.submitBroadcast()
	if a.bc.confirm {
		t.Fatal("asked before one line")
	}
	waitBroadcast(t, a, tt)
	readExactly(t, tt, shells[1], "pwd\r")

	// Refused, the draft stays and is not remembered.
	a.bc.left = map[int]bool{cs[0].id: true, cs[1].id: true}
	a.bc.draft = "uptime"
	a.submitBroadcast()
	if a.bc.draft != "uptime" || len(a.bc.history) != 2 {
		t.Fatalf("refused, draft %q, history %q", a.bc.draft, a.bc.history)
	}
}

func TestBroadcastHistory(t *testing.T) {
	var b broadcastState
	if _, ok := b.recall("x", true); ok {
		t.Fatal("recalled from no history")
	}
	b.remember("one")
	b.remember("two")
	b.remember("two")
	b.remember("  ")
	if len(b.history) != 2 {
		t.Fatalf("history %q", b.history)
	}
	steps := []struct {
		up   bool
		want string
		ok   bool
	}{{true, "two", true}, {true, "one", true}, {true, "", false}, {false, "two", true}, {false, "typed", true}, {false, "", false}}
	for i, s := range steps {
		got, ok := b.recall("typed", s.up)
		if got != s.want || ok != s.ok {
			t.Fatalf("step %d: %q %v, want %q %v", i, got, ok, s.want, s.ok)
		}
	}
	for i := range historyMax + 5 {
		b.remember(strconv.Itoa(i))
	}
	if len(b.history) != historyMax || b.history[len(b.history)-1] != strconv.Itoa(historyMax+4) {
		t.Fatalf("history of %d, last %q", len(b.history), b.history[len(b.history)-1])
	}
}

// keyIn types into terminal pane c as its view takes keys; paste, when
// set, pastes it instead.
func keyIn(a *App, c *conn, ev ui.InputEvent, paste ...string) {
	c.tv.fanout = a.fanoutFrom(c)
	c.tv.holdPaste = func(text string) bool { return a.holdPaste(c, text) }
	if len(paste) > 0 {
		c.tv.Paste(paste[0])
		return
	}
	c.tv.input(ev, nil)
}

func TestSyncKeyboardInput(t *testing.T) {
	a, tt, cs, shells := broadcastPair(t)
	shells[1].out <- "\x1b[?1h"
	waitFor(t, tt, "cursor keys mode", func() bool { return cs[1].s.Term.Modes().AppCursorKeys })
	a.ws.Activate(cs[0].id)
	a.ToggleTool(layout.Broadcast)
	if a.focused() != cs[0] || !a.toolHere(layout.Broadcast) {
		t.Fatalf("focused %v after opening the broadcast pane", a.focused())
	}
	up := ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyUp}
	keyIn(a, cs[0], up)
	readExactly(t, tt, shells[0], "\x1b[A")
	if got := shells[1].take(); got != "" {
		t.Fatalf("typing went to another terminal while not synced: %q", got)
	}

	// Synced: each target gets the key as its terminal reads it.
	a.bc.live = true
	keyIn(a, cs[0], up)
	keyIn(a, cs[0], ui.InputEvent{Kind: ui.InputText, Text: "ls"})
	readExactly(t, tt, shells[0], "\x1b[Als")
	readExactly(t, tt, shells[1], "\x1bOAls")
	if got := shells[2].take(); got != "" {
		t.Fatalf("a terminal of another tab got %q", got)
	}
	// One left out neither gets nor sends.
	a.bc.left = map[int]bool{cs[1].id: true}
	keyIn(a, cs[0], ui.InputEvent{Kind: ui.InputText, Text: "x"})
	keyIn(a, cs[1], ui.InputEvent{Kind: ui.InputText, Text: "y"})
	readExactly(t, tt, shells[0], "x")
	readExactly(t, tt, shells[1], "y")
	a.bc.left = nil

	// Several lines pasted wait for a yes, then go to each as it takes a
	// paste.
	keyIn(a, cs[0], ui.InputEvent{}, "one\ntwo")
	if !a.bc.pasteAsk {
		t.Fatal("several lines pasted without asking")
	}
	if got := shells[0].take() + shells[1].take(); got != "" {
		t.Fatalf("sent before asking: %q", got)
	}
	a.bc.pasteAsk = false
	a.pasteHeld()
	readExactly(t, tt, shells[0], "\x1b[200~one\rtwo\x1b[201~")
	readExactly(t, tt, shells[1], "one\rtwo")

	// Closing the pane ends the sync, which stays off when it opens again.
	a.ToggleTool(layout.Broadcast)
	if a.toolPane(layout.Broadcast) != nil || a.bc.live {
		t.Fatal("the sync outlived its pane")
	}
	a.ToggleTool(layout.Broadcast)
	if a.fanoutFrom(cs[0]) != nil {
		t.Fatal("synced again on its own")
	}
}

func TestToolPanes(t *testing.T) {
	a, _, cs, _ := broadcastPair(t)
	// One of each in the workspace: opened in a tab, then brought to
	// another, then closed where it is.
	a.ToggleTool(layout.Logs)
	logs := a.toolPane(layout.Logs)
	if lt, _ := a.ws.Locate(logs.id); lt != a.ws.TabOf(a.paneNode(cs[2].id)) {
		t.Fatal("the logs pane is not in the active tab")
	}
	a.ws.Activate(cs[0].id)
	a.ToggleTool(layout.Logs)
	if lt, _ := a.ws.Locate(logs.id); a.toolPane(layout.Logs) != logs || lt != a.ws.TabOf(a.paneNode(cs[0].id)) {
		t.Fatal("the logs pane did not come to the active tab")
	}
	if a.focused() != cs[0] {
		t.Fatalf("focused %v, want the terminal", a.focused())
	}
	// A click on it keeps the tab's focus on its session.
	tab := a.ws.ActiveTab()
	tab.Focus = a.paneNode(logs.id)
	a.settleFocus(tab)
	if a.focused() != cs[0] {
		t.Fatalf("focused %v once the tool pane was clicked", a.focused())
	}
	// It is not saved with the layout.
	for _, tb := range a.layoutSnapshot().Tabs {
		var walk func(n *nodeSnap)
		walk = func(n *nodeSnap) {
			if n == nil {
				return
			}
			if n.Kind == "logs" || n.Kind == "broadcast" {
				t.Fatalf("tool pane saved: %+v", n)
			}
			walk(n.A)
			walk(n.B)
		}
		walk(tb.Root)
	}
	a.ToggleTool(layout.Logs)
	if a.toolPane(layout.Logs) != nil {
		t.Fatal("the logs pane stayed open")
	}
}
