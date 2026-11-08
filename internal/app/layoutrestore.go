package app

import (
	"encoding/json"
	"errors"
	"log"
	"os"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// panelNowPane are the panels a saved layout may still list that are
// panes now, the snippets and the command history: left out, not an
// error.
var panelNowPane = map[inspectorKind]bool{"snippets": true, "history": true}

func (a *App) loadLayout() {
	file := a.layoutPath()
	if file == "" || !a.settings.RememberLayout || len(a.ws.Tabs) > 0 {
		return
	}
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var snap layoutSnap
	if err == nil {
		err = json.Unmarshal(b, &snap)
	}
	if err == nil && legacyLayout(b) {
		// Legacy AI-only files are not sessions; start recording the current workspace.
		a.rememberLayoutBaseline()
		return
	}
	if err != nil || !validLayout(snap) {
		a.layoutBlocked = true
		a.layoutNotice(true)
		log.Printf("sshyard: workspace layout read failed or unsupported version")
		return
	}
	a.restoring = true
	defer func() { a.restoring = false }()
	mapping := map[int]*layout.Node{}
	links := map[int]int{}
	skipped := false
	active := 0
	for i, t := range snap.Tabs {
		seen := map[string]bool{}
		root := a.restoreLayoutNode(t.Root, mapping, links, seen, &skipped)
		if root == nil {
			continue
		}
		focus := 0
		if p := mapping[t.Focus]; p != nil {
			focus = p.ID
		}
		if i == snap.Active {
			active = len(a.ws.Tabs)
		}
		a.ws.AddTab(root, focus)
		a.ws.ActiveTab().Title = t.Title
	}
	if len(a.ws.Tabs) > 0 {
		a.ws.Active = active
		a.focusReq = a.ws.ActiveTab().Focused().ID
	}
	for id, link := range links {
		ai := a.conns[id]
		if p := mapping[link]; p != nil {
			term := a.conns[p.Item.ID]
			if term != nil && term.kind == layout.Terminal && term.host.ID == ai.host.ID {
				ai.ai.link = term.id
			}
		}
	}
	for k, v := range snap.Sections {
		value := v
		a.sections[k] = &value
	}
	a.panels = nil
	for _, k := range snap.Panels {
		if !panelNowPane[k] {
			a.panels = append(a.panels, k)
		}
	}
	for k, v := range snap.StackPct {
		value := v
		a.stackPct[k] = &value
	}
	if skipped {
		a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast { return ui.Toast{Title: m.LayoutRestoreSkipped(), Type: "warning"} })
	}
	// All tabs and references exist before any fresh session starts.
	for _, c := range a.conns {
		if c.s == nil {
			continue
		}
		c.restoredQuiet = true
		if a.restoredConnectionNeedsSecrets(c.host) {
			c.waitingSecrets = true
			c.restorePending = true
			continue
		}
		c.s.Start()
	}
	// Establish the restored baseline, without rewriting the file on launch.
	a.rememberLayoutBaseline()
}

// Establish a baseline only when serialization succeeds, retaining diagnostics on failure.
func (a *App) rememberLayoutBaseline() {
	b, err := a.layoutBytes()
	if err != nil {
		if !a.layoutSaveFailed {
			a.layoutNotice(false)
		}
		a.layoutSaveFailed = true
		log.Printf("sshyard: workspace layout baseline serialization failed")
		return
	}
	a.layoutObserved = b
}

// Only the known unversioned workspace envelope is treated as a legacy file.
func legacyLayout(b []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		return false
	}
	if _, versioned := fields["version"]; versioned {
		return false
	}
	tabs, ok := fields["tabs"]
	if !ok {
		return false
	}
	var old []json.RawMessage
	return json.Unmarshal(tabs, &old) == nil
}

// Validate the complete tree before allocating sessions; unknown versions remain untouched.
func validLayout(s layoutSnap) bool {
	if s.Version != 1 || len(s.Tabs) > 256 {
		return false
	}
	ids := map[int]bool{}
	count := 0
	var valid func(*nodeSnap, int) bool
	valid = func(n *nodeSnap, depth int) bool {
		count++
		if n == nil || depth > 64 || count > 4096 {
			return false
		}
		if n.Split != "" {
			return (n.Split == "row" || n.Split == "column") && n.Kind == "" && n.Ratio > 0 && n.Ratio < 1 && valid(n.A, depth+1) && valid(n.B, depth+1)
		}
		if n.A != nil || n.B != nil || n.Host == "" || n.ID <= 0 || ids[n.ID] {
			return false
		}
		ids[n.ID] = true
		return n.Kind == "terminal" || n.Kind == "files" || n.Kind == "ai" || n.Kind == "editor"
	}
	for _, t := range s.Tabs {
		if !valid(t.Root, 0) {
			return false
		}
	}
	panelSeen := map[inspectorKind]bool{}
	for _, p := range s.Panels {
		if panelNowPane[p] {
			continue
		}
		known := false
		for _, k := range panelOrder {
			known = known || k == p
		}
		if !known || panelSeen[p] {
			return false
		}
		panelSeen[p] = true
	}
	for _, v := range s.StackPct {
		if v <= 0 || v >= 100 {
			return false
		}
	}
	return true
}
func (a *App) restoreLayoutNode(n *nodeSnap, mapping map[int]*layout.Node, links map[int]int, seen map[string]bool, skipped *bool) *layout.Node {
	if n.Split != "" {
		left := a.restoreLayoutNode(n.A, mapping, links, seen, skipped)
		right := a.restoreLayoutNode(n.B, mapping, links, seen, skipped)
		if left == nil {
			return right
		}
		if right == nil {
			return left
		}
		axis := layout.Row
		if n.Split == "column" {
			axis = layout.Column
		}
		return &layout.Node{ID: a.ws.FreshID(), Axis: axis, Ratio: n.Ratio, A: left, B: right}
	}
	if n.Kind == "editor" || isTemp(n.Host) || isLocal(n.Host) && !a.layoutShellExists(n.Host) || (n.Kind == "ai" || n.Kind == "files") && seen[n.Kind] {
		*skipped = true
		return nil
	}
	kind := layout.Terminal
	switch n.Kind {
	case "ai":
		kind = layout.AI
	case "files":
		kind = layout.Files
	}
	c, err := a.makeConn(n.Host, kind, 0)
	if err != nil {
		*skipped = true
		return nil
	}
	seen[n.Kind] = true
	if c.ai != nil {
		if n.Agent != "" {
			c.ai.agent = n.Agent
		}
		if n.Mode != "full" {
			c.ai.mode = n.Mode
		}
		c.ai.provider, c.ai.model, c.ai.effort = n.Provider, n.Model, n.Effort
		links[c.id] = n.Link
	}
	p := &layout.Node{ID: a.ws.FreshID(), Item: c.item()}
	mapping[n.ID] = p
	return p
}
func (a *App) layoutShellExists(id string) bool {
	for _, sh := range a.shells {
		if localID+sh.ID == id {
			return true
		}
	}
	return false
}
func (a *App) startRestoredConnections() {
	for _, c := range a.conns {
		if c.restorePending && c.s != nil {
			c.restorePending = false
			c.waitingSecrets = false
			c.s.Start()
		}
	}
}
func (a *App) ReportLayoutReadError(err error) {
	if err != nil {
		log.Printf("sshyard: native layout read failed")
		a.post(func() { a.layoutNotice(true) })
	}
}

// Remembered local-key metadata covers automatic AuthAsk credentials without
// discovering private key files or requesting authorization during restoration.
func (a *App) restoredConnectionNeedsSecrets(h assets.Host) bool {
	if a.connectionNeedsSecrets(h) {
		return true
	}
	if a.unlocked() || !a.unlock.stored || !a.lkOn.Load() || isLocal(h.ID) {
		return false
	}
	path, _ := a.store.JumpPath(h.ID)
	path = append(path, h)
	for _, hop := range path {
		if hop.Auth == assets.AuthAsk && a.lk.IsRemembered(a.lk.Last(hop.ID)) {
			return true
		}
	}
	return false
}
