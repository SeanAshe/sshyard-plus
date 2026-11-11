package app

import (
	"math"
	"testing"

	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/egoist/mygo/ui"
)

// TestRailPaneKeepsWidth: a pane the rail opens comes back as wide as the
// user left it beside another pane, not at half the room, and the layout
// keeps that width for the next launch.
func TestRailPaneKeepsWidth(t *testing.T) {
	dir, backend := t.TempDir(), keychain.NewMemory()
	rememberLayoutIn(t, dir)
	a, _ := appIn(t, dir, backend)
	h := savedHost(t, a, "saved", "127.0.0.1")
	term, err := a.makeConn(h.ID, layout.Terminal, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.ws.Open(term.item())
	a.ToggleTool(layout.Monitor)
	if r := a.ws.ActiveTab().Root; r.Ratio != 0.5 {
		t.Fatalf("first opened at %v, want half", r.Ratio)
	}
	// Dragged to 296 wide beside the terminal, then closed.
	mon := a.toolPane(layout.Monitor)
	a.paneBounds[term.id], a.paneBounds[mon.id] = ui.Rect{W: 896}, ui.Rect{W: 296}
	a.ToggleTool(layout.Monitor)
	if w := a.railWidths[layout.Monitor]; w != 296 {
		t.Fatalf("remembered %v, want 296", w)
	}
	// Opened again beside the terminal, which has the whole room.
	a.paneBounds[term.id] = ui.Rect{W: 1196}
	a.ToggleTool(layout.Monitor)
	mon = a.toolPane(layout.Monitor)
	r := a.ws.ActiveTab().Root
	want := 1 - float32(296+paneGap)/float32(1196+paneGap-1)
	if r.Leaf() || r.B.Item.ID != mon.id || math.Abs(float64(r.Ratio-want)) > 1e-4 {
		t.Fatalf("reopened at %v, want %v", r.Ratio, want)
	}
	// Alone in a tab its width is the tab's, which is not remembered.
	a.ws.Detach(mon.id)
	a.paneBounds[mon.id] = ui.Rect{W: 1196}
	a.CloseSession(mon.id)
	if w := a.railWidths[layout.Monitor]; w != 296 {
		t.Fatalf("remembered %v from a pane alone in its tab", w)
	}
	// Never more than most of the room.
	a.paneBounds[term.id] = ui.Rect{W: 2396}
	a.railWidths[layout.Usage] = 5000
	a.ToggleTool(layout.Usage)
	if r := a.ws.ActiveTab().Root; math.Abs(float64(r.Ratio)-0.15) > 1e-4 {
		t.Fatalf("opened at %v in a narrow room", r.Ratio)
	}
	if got := a.layoutSnapshot().Widths; got["monitor"] != 296 || got["usage"] != 5000 {
		t.Fatalf("saved widths %v", got)
	}
	a.Close()
	again, _ := appIn(t, dir, backend)
	if w := again.railWidths[layout.Monitor]; w != 296 {
		t.Fatalf("restored %v, want 296", w)
	}
}

// TestRailWidthsRestoreKnownKinds: only the widths of kinds the rail
// opens, in range, come back from the layout file.
func TestRailWidthsRestoreKnownKinds(t *testing.T) {
	for s, want := range map[string]bool{"files": true, "ai": true, "docker": true, "terminal": false, "editor": false, "bogus": false} {
		if _, ok := railKind(s); ok != want {
			t.Fatalf("railKind(%q) = %v, want %v", s, ok, want)
		}
	}
}
