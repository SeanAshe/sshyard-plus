package app

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
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
	s := a.Settings()
	s.ToolPanes = settings.ToolPanesSplit
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
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
	// None takes the focus; the layout saves where each is, on no host.
	a.ToggleTool(layout.Snippets)
	a.ToggleTool(layout.History)
	if a.focused() != cs[0] || a.snippetTarget() != cs[0] {
		t.Fatalf("focused %v once the snippets and history opened", a.focused())
	}
	saved := map[string]int{}
	for _, tb := range a.layoutSnapshot().Tabs {
		var walk func(n *nodeSnap)
		walk = func(n *nodeSnap) {
			if n == nil {
				return
			}
			if n.Kind == "logs" || n.Kind == "broadcast" || n.Kind == "snippets" || n.Kind == "history" {
				if n.Host != "" {
					t.Fatalf("tool pane saved with a host: %+v", n)
				}
				saved[n.Kind]++
			}
			walk(n.A)
			walk(n.B)
		}
		walk(tb.Root)
	}
	if saved["logs"] != 1 || saved["snippets"] != 1 || saved["history"] != 1 {
		t.Fatalf("tool panes saved %v, want logs, snippets and history once", saved)
	}
	a.ToggleTool(layout.Logs)
	if a.toolPane(layout.Logs) != nil {
		t.Fatal("the logs pane stayed open")
	}
}

// TestToolPanesAsTabs: with the rail's panes as tabs, the default, a
// tool, files or AI pane the rail opens, or brings from another tab,
// takes the place of the one the tab shows, which closes; the focus stays
// on the session.
func TestToolPanesAsTabs(t *testing.T) {
	a, _, cs, _ := broadcastPair(t)
	if a.Settings().ToolPanes == settings.ToolPanesSplit {
		t.Fatal("the rail's panes open side by side by default")
	}
	a.ToggleTool(layout.Snippets)
	tab := a.ws.ActiveTab()
	panes := len(tab.Panes())
	a.ToggleTool(layout.History)
	if a.toolPane(layout.Snippets) != nil || tab.Count(layout.History) != 1 || len(tab.Panes()) != panes {
		t.Fatalf("history did not replace the snippets: %v", tab.Items())
	}
	if a.focused() != cs[2] {
		t.Fatalf("focused %v, want the terminal", a.focused())
	}
	// From another tab, the logs come in the place of its history.
	a.ws.Activate(cs[2].id)
	a.ToggleTool(layout.Logs)
	logs := a.toolPane(layout.Logs)
	a.ws.Activate(cs[0].id)
	other := a.ws.ActiveTab()
	a.ToggleTool(layout.Snippets)
	n := len(other.Panes())
	a.ToggleTool(layout.Logs)
	if lt, _ := a.ws.Locate(logs.id); lt != other || a.toolPane(layout.Snippets) != nil || len(other.Panes()) != n {
		t.Fatalf("the logs did not replace the snippets: %v", other.Items())
	}
	if a.toolPane(layout.Logs) != logs || a.focused() != cs[0] {
		t.Fatalf("logs %v, focused %v", a.toolPane(layout.Logs), a.focused())
	}
	// The tool the tab shows still closes on its own.
	a.ToggleTool(layout.Logs)
	if a.toolPane(layout.Logs) != nil || len(other.Panes()) != n-1 {
		t.Fatalf("the logs stayed open: %v", other.Items())
	}
	// The AI pane is one of the rail's as the tools are.
	a.ToggleTool(layout.Tmux)
	n = len(other.Panes())
	a.ToggleAI()
	if a.toolPane(layout.Tmux) != nil || other.Count(layout.AI) != 1 || len(other.Panes()) != n {
		t.Fatalf("the assistant did not replace tmux: %v", other.Items())
	}
	a.ToggleTool(layout.Monitor)
	if other.Count(layout.AI) != 0 || other.Count(layout.Monitor) != 1 || len(other.Panes()) != n {
		t.Fatalf("the monitor did not replace the assistant: %v", other.Items())
	}
	if a.focused() != cs[0] {
		t.Fatalf("focused %v, want the terminal", a.focused())
	}
}

// TestToolPanesStack: side by side, the rail opens three columns beside
// the session, then each pane past them under a column not split yet; a
// column closed makes room for a new one, the AI pane goes as the tools,
// and the tools leave the focus on the session.
func TestToolPanesStack(t *testing.T) {
	a, tt, cs, _ := broadcastPair(t)
	s := a.Settings()
	s.ToolPanes = settings.ToolPanesSplit
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	// Room for three columns beside the session.
	tt.SetSize(2400, 900)
	tt.Frame()
	tab := a.ws.ActiveTab()
	rail := []layout.Kind{layout.Snippets, layout.History, layout.Logs}
	for _, k := range rail {
		a.ToggleTool(k)
		if railAbove(a, tab, k) != nil {
			t.Fatalf("%v opened under another pane: %v", k, tab.Items())
		}
	}
	a.ToggleTool(layout.Monitor)
	first := railAbove(a, tab, layout.Monitor)
	if first == nil || !isTool(first.Item.Kind) {
		t.Fatalf("the monitor did not open under a tool: %v", tab.Items())
	}
	// One of the other two columns closed, the next opens as a column.
	for _, k := range rail {
		if k != first.Item.Kind {
			a.ToggleTool(k)
			break
		}
	}
	a.ToggleTool(layout.Usage)
	if railAbove(a, tab, layout.Usage) != nil {
		t.Fatalf("the usage did not open as a column: %v", tab.Items())
	}
	if a.focused() != cs[2] {
		t.Fatalf("focused %v, want the terminal", a.focused())
	}
	a.ToggleAI()
	second := railAbove(a, tab, layout.AI)
	if second == nil || !isTool(second.Item.Kind) || second.Item.ID == first.Item.ID {
		t.Fatalf("the assistant did not open under a column not split yet: %v", tab.Items())
	}
	if len(tab.Panes()) != 6 {
		t.Fatalf("panes %v, want the session and five of the rail's", tab.Items())
	}
}

// TestToolPanesFit: side by side, a column that would leave one of the
// row's too narrow shares the row out evenly, and one with no room even
// then opens under a column of the rail's, by the session's row even
// once a pane of the rail's has the focus.
func TestToolPanesFit(t *testing.T) {
	a, _, cs, _ := broadcastPair(t)
	s := a.Settings()
	s.ToolPanes = settings.ToolPanesSplit
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tab := a.ws.ActiveTab()
	a.paneBounds[cs[2].id] = ui.Rect{W: 1000}
	a.ToggleTool(layout.Snippets)
	if r := tab.Root; r.Ratio != 0.5 {
		t.Fatalf("snippets opened at %v, want half", r.Ratio)
	}
	snippets := a.toolPane(layout.Snippets)
	a.paneBounds[cs[2].id], a.paneBounds[snippets.id] = ui.Rect{W: 496}, ui.Rect{X: 500, W: 500}
	a.ToggleTool(layout.History)
	r := tab.Root
	if r.Leaf() || r.A.Leaf() || math.Abs(float64(r.Ratio)-2.0/3) > 1e-4 || r.A.Ratio != 0.5 {
		t.Fatalf("the row was not shared out evenly: %+v", r)
	}
	history := a.toolPane(layout.History)
	a.paneBounds[cs[2].id], a.paneBounds[history.id], a.paneBounds[snippets.id] = ui.Rect{W: 330}, ui.Rect{X: 334, W: 330}, ui.Rect{X: 668, W: 332}
	a.ToggleTool(layout.Logs)
	if above := railAbove(a, tab, layout.Logs); above == nil || above.Item.ID != history.id {
		t.Fatalf("the logs did not open under the history with no room: %v", tab.Items())
	}
	// The assistant takes the focus; what opens next still goes by the
	// session's row, under the column the logs left whole.
	a.ToggleAI()
	if above := railAbove(a, tab, layout.AI); above == nil || above.Item.ID != snippets.id || a.focused().kind != layout.AI {
		t.Fatalf("the assistant did not open under the snippets: %v, focused %v", tab.Items(), a.focused())
	}
	a.ToggleTool(layout.Tmux)
	if above := railAbove(a, tab, layout.Tmux); above == nil || above.Item.ID != history.id || a.focused().kind != layout.AI {
		t.Fatalf("tmux did not open under the history: %v, focused %v", tab.Items(), a.focused())
	}
}

// railAbove is the pane over the one of kind k in tab, nil when it has
// none.
func railAbove(a *App, tab *layout.Tab, k layout.Kind) *layout.Node {
	_, p := a.ws.Locate(a.tabPane(tab, k).id)
	if sp := tab.Parent(p); sp != nil && sp.Axis == layout.Column && sp.B == p && sp.A.Leaf() {
		return sp.A
	}
	return nil
}
