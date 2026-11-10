package app

import (
	"strconv"
	"time"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// clipEffect is what one OSC 52 does to a pane. The clipboard text stays
// in memory only, on the gate, until undo or 30s.
type clipEffect struct {
	Write bool // put the paste on the clipboard; Fresh means snapshot first
	Fresh bool
	Ask   bool
	Pause bool
	Query bool // a read, always refused
	Limit bool // dropped for size
}

// clipPane is one terminal pane's OSC 52 state. Touched only on the UI thread.
type clipPane struct {
	writes    []time.Time
	paused    bool
	pauseTold bool
	session   bool // always allow, until the pane closes
	lastWrite time.Time
	undo      string
	undoSet   bool
	undoUntil time.Time
	wrote     string // last text this pane put on the clipboard
	queryAt   time.Time
	queryTold bool
}

// clipGate decides writes, asks, pauses and undo. now, when set, replaces
// the clock (tests).
type clipGate struct {
	now   func() time.Time
	panes map[int]*clipPane
}

func (g *clipGate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *clipGate) pane(id int) *clipPane {
	if g.panes == nil {
		g.panes = map[int]*clipPane{}
	}
	p := g.panes[id]
	if p == nil {
		p = &clipPane{}
		g.panes[id] = p
	}
	return p
}

func (g *clipGate) peek(id int) *clipPane {
	if g.panes == nil {
		return nil
	}
	return g.panes[id]
}

// incoming classifies one event. data is the decoded paste; query is a
// read; drop is an over-limit paste. policy is allow, ask or deny.
func (g *clipGate) incoming(pane int, policy, data string, query, drop bool) clipEffect {
	now := g.clock()
	p := g.pane(pane)
	if p.undoSet && !p.undoLive(now) {
		p.clearUndo()
	}
	if query {
		if p.queryTold && now.Sub(p.queryAt) < time.Minute {
			return clipEffect{}
		}
		p.queryAt, p.queryTold = now, true
		return clipEffect{Query: true}
	}
	if drop {
		if policy == settings.OSC52Deny {
			return clipEffect{}
		}
		return clipEffect{Limit: true}
	}
	if data == "" || policy == settings.OSC52Deny {
		return clipEffect{}
	}
	if p.paused {
		if p.pauseTold {
			return clipEffect{}
		}
		p.pauseTold = true
		return clipEffect{Pause: true}
	}
	if policy == settings.OSC52Ask && !p.session {
		return clipEffect{Ask: true}
	}
	return g.beginWrite(p, now)
}

// beginWrite records a write, or pauses the pane when this is the 11th
// in 10 seconds. The 11th is not written.
func (g *clipGate) beginWrite(p *clipPane, now time.Time) clipEffect {
	cut := now.Add(-10 * time.Second)
	kept := p.writes[:0]
	for _, at := range p.writes {
		if !at.Before(cut) {
			kept = append(kept, at)
		}
	}
	p.writes = kept
	if len(p.writes) >= 10 {
		p.paused, p.pauseTold = true, true
		return clipEffect{Pause: true}
	}
	p.writes = append(p.writes, now)
	fresh := !p.undoSet || now.Sub(p.lastWrite) >= time.Second
	if fresh {
		p.undoUntil = now.Add(30 * time.Second)
	}
	p.lastWrite = now
	return clipEffect{Write: true, Fresh: fresh}
}

// allow confirms the paste in front of an ask. always remembers the pane
// for this run.
func (g *clipGate) allow(pane int, always bool) clipEffect {
	p := g.pane(pane)
	if always {
		p.session = true
	}
	if p.paused {
		if p.pauseTold {
			return clipEffect{}
		}
		p.pauseTold = true
		return clipEffect{Pause: true}
	}
	return g.beginWrite(p, g.clock())
}

// undoLive reports whether the pre-burst snapshot can still be restored.
// It ends at undoUntil, which is 30s from the first write of the burst.
func (p *clipPane) undoLive(now time.Time) bool {
	return p != nil && p.undoSet && now.Before(p.undoUntil)
}

func (p *clipPane) clearUndo() {
	p.undo, p.undoSet, p.wrote = "", false, ""
}

func (g *clipGate) noteUndo(pane int, old string) {
	p := g.pane(pane)
	p.undo, p.undoSet = old, true
}

// noteWrote remembers the text just written, so undo can tell a later
// local copy from this pane's own write.
func (g *clipGate) noteWrote(pane int, text string) {
	g.pane(pane).wrote = text
}

func (g *clipGate) takeUndo(pane int) (old, wrote string, ok bool) {
	p := g.peek(pane)
	if !p.undoLive(g.clock()) {
		if p != nil {
			p.clearUndo()
		}
		return "", "", false
	}
	old, wrote = p.undo, p.wrote
	p.clearUndo()
	return old, wrote, true
}

func (g *clipGate) dropUndo(pane int) {
	if p := g.peek(pane); p != nil {
		p.clearUndo()
	}
}

// undoLeft is how long the snapshot stays restorable.
func (g *clipGate) undoLeft(pane int) time.Duration {
	p := g.peek(pane)
	if !p.undoLive(g.clock()) {
		return 0
	}
	return p.undoUntil.Sub(g.clock())
}

// expireUndo drops snapshots whose 30s is over. dead are the panes whose
// write toast should close. next is the earliest snapshot still live.
func (g *clipGate) expireUndo(now time.Time) (dead []int, next time.Time) {
	for id, p := range g.panes {
		if p == nil || !p.undoSet {
			continue
		}
		if !p.undoLive(now) {
			p.clearUndo()
			dead = append(dead, id)
			continue
		}
		if next.IsZero() || p.undoUntil.Before(next) {
			next = p.undoUntil
		}
	}
	return dead, next
}

// resume lets the pane write again. The 10-second window resets, or the
// next write would pause at once.
func (g *clipGate) resume(pane int) {
	p := g.peek(pane)
	if p == nil {
		return
	}
	p.paused, p.pauseTold = false, false
	p.writes = nil
}

func (g *clipGate) clearPauseTold(pane int) {
	if p := g.peek(pane); p != nil {
		p.pauseTold = false
	}
}

func (g *clipGate) forget(pane int) { delete(g.panes, pane) }

func oscID(kind string, pane int) string {
	return "osc52-" + kind + "-" + strconv.Itoa(pane)
}

// armOSC52 sends the pane's clipboard reports to the UI thread.
func (a *App) armOSC52(c *conn) {
	term := c.s.Term
	term.SetOSC52Max(a.settings.OSC52MaxBytes)
	id := c.id
	term.OnClipboard = func(_, data string, query bool) {
		a.post(func() { a.deliverOSC52(id, data, query, false) })
	}
	term.OnClipboardDrop = func() {
		a.post(func() { a.deliverOSC52(id, "", false, true) })
	}
}

// deliverOSC52 applies one report. The host is read again so a saved
// override reaches a pane that is already open.
func (a *App) deliverOSC52(id int, data string, query, drop bool) {
	c := a.conns[id]
	if c == nil {
		return
	}
	h := c.host
	if live, ok := a.host(c.host.ID); ok {
		h = live
	}
	policy := settings.ResolveOSC52Write(a.settings.OSC52Write, h.OSC52)
	a.applyClip(id, h.Label(), data, a.clip.incoming(id, policy, data, query, drop))
}

func (a *App) applyClip(id int, host, data string, eff clipEffect) {
	switch {
	case eff.Query:
		a.toast(func(m locales.Messages) ui.Toast {
			return ui.Toast{ID: oscID("read", id), Title: m.Osc52Refused(host)}
		})
	case eff.Limit:
		a.toast(func(m locales.Messages) ui.Toast {
			return ui.Toast{ID: oscID("limit", id), Title: m.Osc52TooBig(host), Type: "error"}
		})
	case eff.Pause:
		a.toast(func(m locales.Messages) ui.Toast {
			return ui.Toast{
				ID: oscID("pause", id), Title: m.Osc52Paused(), Action: m.Osc52Resume(), Timeout: -1,
				OnAction: func() { a.clip.resume(id) },
				OnClose:  func() { a.clip.clearPauseTold(id) },
			}
		})
	case eff.Ask:
		a.clipAsk = &oscAsk{open: true, pane: id, host: host, data: data}
	case eff.Write:
		if !a.settings.OSC52Notify {
			// No notice means no Undo, so nothing is snapshotted.
			a.svc.WriteClipboard(data)
			return
		}
		if eff.Fresh {
			a.clip.noteUndo(id, a.svc.ReadClipboard())
		}
		a.clip.noteWrote(id, data)
		a.svc.WriteClipboard(data)
		n := strconv.Itoa(len(data))
		// The toast lasts until the snapshot does, not a fresh 30s from
		// this write: a merged write must not keep a dead Undo.
		left := a.clip.undoLeft(id)
		a.toast(func(m locales.Messages) ui.Toast {
			t := ui.Toast{
				ID: oscID("write", id), Title: m.Osc52Wrote(host, n),
				OnClose: func() { a.clip.dropUndo(id) },
			}
			if left > 0 {
				t.Timeout = left
				t.Action = m.Osc52Undo()
				t.OnAction = func() { a.undoOSC52(id) }
			}
			return t
		})
	}
}

// wakeUndo closes a write toast when its snapshot is discarded. The
// toast timer pauses while the pointer rests on it, so this asks for a
// frame at the absolute deadline.
func (a *App) wakeUndo(c *ui.Context) {
	dead, next := a.clip.expireUndo(c.Now())
	for _, id := range dead {
		c.CloseToast(oscID("write", id))
	}
	if !next.IsZero() {
		c.After(next.Sub(c.Now()))
	}
}

func (a *App) undoOSC52(id int) {
	old, wrote, ok := a.clip.takeUndo(id)
	if !ok {
		return
	}
	if a.svc.ReadClipboard() != wrote {
		a.toast(func(m locales.Messages) ui.Toast {
			return ui.Toast{ID: oscID("undone", id), Title: m.Osc52UndoGone()}
		})
		return
	}
	a.svc.WriteClipboard(old)
}

// oscPaneName is the ask dialog's name for a pane. The host's own label
// is shown beside it. A remote title (OSC 0/2, or anything the server
// sends) is never used: the only pane identity here is the local number
// from conn.termNo, and it is hidden when that host has one terminal open.
func (a *App) oscPaneName(m locales.Messages, id int) string {
	c := a.conns[id]
	if c == nil || c.termNo == 0 || a.hostTermPanes(c.host.ID) <= 1 {
		return m.Terminal()
	}
	return m.Osc52AskPane(strconv.Itoa(c.termNo))
}

// hostTermPanes is how many terminal panes of hostID are open.
func (a *App) hostTermPanes(hostID string) int {
	n := 0
	for _, o := range a.conns {
		if o.termNo > 0 && o.host.ID == hostID {
			n++
		}
	}
	return n
}

// oscAsk is the ask-mode dialog. data is the paste, in memory only.
type oscAsk struct {
	open bool
	pane int
	host string
	data string
}

func (a *App) finishAsk(ask *oscAsk, always bool) {
	id, data, host := ask.pane, ask.data, ask.host
	ask.open = false
	if a.conns[id] == nil {
		return
	}
	a.applyClip(id, host, data, a.clip.allow(id, always))
}

func (a *App) oscAskView(c *ui.Context, m locales.Messages) {
	ask := a.clipAsk
	if ask == nil {
		return
	}
	tok := currentTokens(c)
	sheet(ui.Modal(c, &ask.open, func() {
		ui.Column(c).Gap(14).Width(440).Children(func() {
			sheetTitle(c, m.Osc52AskTitle())
			ui.Text(c, m.Osc52AskFrom(ask.host, a.oscPaneName(m, ask.pane))).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			ui.Text(c, m.Osc52AskBytes(strconv.Itoa(len(ask.data)))).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			ui.Text(c, clipPreview(ask.data)).FontSize(theme.Body)
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				if pill(c, m.Osc52Deny(), false, false, false) {
					ask.open = false
				}
				ui.Spacer(c)
				if pill(c, m.Osc52AllowSession(), false, false, false) {
					a.finishAsk(ask, true)
				}
				if pill(c, m.Osc52AllowOnce(), true, false, false) {
					a.finishAsk(ask, false)
				}
			})
		})
	}), tok)
	if !ask.open && a.clipAsk == ask {
		a.clipAsk = nil
	}
}

// clipPreview is the first 200 runes of a paste, made safe to show.
// The ellipsis sits outside that escaping.
func clipPreview(s string) string {
	r := []rune(s)
	more := len(r) > 200
	if more {
		r = r[:200]
	}
	out := shownName(string(r))
	if more {
		out += "…"
	}
	return out
}
