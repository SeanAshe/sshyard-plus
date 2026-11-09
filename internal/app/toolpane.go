package app

import (
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The tool panes, the broadcast, the session logs, the snippets, the
// command history and the panels on the focused session's host (the
// system monitor, the process usage, the host's details, Docker and
// tmux), work on the sessions, not on one of their own. They are panes as the others are, dragged by their
// header to any edge of a pane or another tab, but on no host, and one of
// each in the whole workspace, which the rail opens and closes. They are
// not saved with the layout. A tool pane never keeps a tab's focus: the
// focused pane stays the session the panels and the shortcuts work on.

// isTool reports a tool pane's kind.
func isTool(k layout.Kind) bool {
	switch k {
	case layout.Broadcast, layout.Logs, layout.Snippets, layout.History,
		layout.Monitor, layout.Usage, layout.HostInfo, layout.Docker, layout.Tmux:
		return true
	}
	return false
}

// toolPane is the tool pane of kind k, nil when it is not open.
func (a *App) toolPane(k layout.Kind) *conn {
	for _, c := range a.conns {
		if c.kind == k {
			return c
		}
	}
	return nil
}

// toolHere reports whether the tool pane of kind k is in the active tab.
func (a *App) toolHere(k layout.Kind) bool {
	c := a.toolPane(k)
	if c == nil {
		return false
	}
	t, _ := a.ws.Locate(c.id)
	return t != nil && t == a.ws.ActiveTab()
}

// connLabel names pane cn: its host, or what a tool pane is.
func (a *App) connLabel(cn *conn) string {
	if isTool(cn.kind) {
		return a.toolNames[cn.kind]
	}
	return a.pv(cn.host.Label())
}

// nameTools keeps what the tool panes are called in m's language.
func (a *App) nameTools(m locales.Messages) {
	if a.toolNames == nil {
		a.toolNames = map[layout.Kind]string{}
	}
	a.toolNames[layout.Broadcast] = m.Broadcast()
	a.toolNames[layout.Logs] = m.SessionLogs()
	a.toolNames[layout.Snippets] = m.Snippets()
	a.toolNames[layout.History] = m.CommandHistory()
	a.toolNames[layout.Monitor] = m.SystemMonitor()
	a.toolNames[layout.Usage] = m.ProcessUsage()
	a.toolNames[layout.HostInfo] = m.HostDetails()
	a.toolNames[layout.Docker] = m.DockerManager()
	a.toolNames[layout.Tmux] = m.TmuxManager()
}

// ShowTool opens the tool pane of kind k in the active tab, or brings it
// there, leaving it open when it already is.
func (a *App) ShowTool(k layout.Kind) {
	if !a.toolHere(k) {
		a.ToggleTool(k)
	}
}

// ToggleTool closes the tool pane of kind k when it is in the active tab;
// else it opens it, or brings it from the tab it is in, beside the
// focused pane.
func (a *App) ToggleTool(k layout.Kind) {
	t := a.ws.ActiveTab()
	a.settleFocus(t)
	defer func() { a.settleFocus(a.ws.ActiveTab()) }()
	if c := a.toolPane(k); c != nil {
		if ct, _ := a.ws.Locate(c.id); ct == t || t == nil {
			a.CloseSession(c.id)
			return
		}
		a.ws.Move(c.id, t.Focus, layout.Right)
		a.focusReq = c.id
		a.page = pageWorkspace
		a.saveLayout()
		return
	}
	if k == layout.Logs {
		a.listLogs()
	}
	a.nextConn++
	c := &conn{id: a.nextConn, kind: k}
	a.conns[c.id] = c
	if t == nil || !a.ws.Place(c.item(), t.Focus, layout.Right) {
		a.ws.Open(c.item())
	}
	a.focusReq = c.id
	a.page = pageWorkspace
	a.saveLayout()
}

// settleFocus keeps tab t's focus off its tool panes: on the pane it
// focused last, or else its first other pane. A tab of tool panes alone
// keeps it where it is.
func (a *App) settleFocus(t *layout.Tab) {
	if t == nil {
		return
	}
	if a.toolFocus == nil {
		a.toolFocus = map[int]int{}
	}
	if !isTool(t.Focused().Kind) {
		a.toolFocus[t.ID] = t.Focus
		return
	}
	if p := t.Pane(a.toolFocus[t.ID]); p != nil && !isTool(p.Item.Kind) {
		t.Focus = p.ID
		return
	}
	for _, p := range t.Panes() {
		if !isTool(p.Item.Kind) {
			t.Focus = p.ID
			return
		}
	}
}

// toolPaneView is tool pane cn: its header, which drags it, with the
// pane's own actions, over what it shows.
func (a *App) toolPaneView(c *ui.Context, cn *conn, tok theme.Tokens, m locales.Messages) {
	// Opened, or its tab chosen: the keyboard goes to its field, if any.
	focus := a.focusReq == cn.id && a.page == pageWorkspace
	if focus {
		a.focusReq = 0
	}
	head := ui.Row(c.Key("head")).Height(paneHeadHeight).Shrink(0).Padding(0, 6, 0, 10).Gap(8).AlignItems(ui.Center).
		Drag(dragSession{ID: cn.id}).Label(m.PaneActions())
	head.Children(func() {
		softTile(c, kindIcon(cn.kind, true), tok.Accent, paneHeadIcon)
		ui.Text(c, a.connLabel(cn)).FontWeight(600).FontSize(theme.Footnote).SingleLine().Shrink(1).MinWidth(0)
		switch cn.kind {
		case layout.Snippets:
			a.snippetsHead(c, m, tok)
		case layout.History:
			a.historyHead(c, m, tok)
		default:
			ui.Spacer(c)
		}
		if (IconButton{Icon: icons.Close, Label: m.ClosePane(), Size: Tiny, Danger: true}).Clicked(c, "close") {
			a.closeSoon(cn.id)
		}
	})
	ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
	ui.Column(c.Key("body")).Grow(1).Shrink(1).MinHeight(0).FillWidth().Children(func() {
		switch cn.kind {
		case layout.Broadcast:
			a.broadcastPanel(c, m, tok, focus)
		case layout.Logs:
			a.logPanel(c, m, tok)
		case layout.Snippets:
			a.snippetsPanel(c, m, tok)
		case layout.History:
			a.historyPanel(c, m, tok)
		default:
			a.hostToolPanel(c, m, tok, cn.kind)
		}
	})
}
