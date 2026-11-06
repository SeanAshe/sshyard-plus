package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// The tmux panel: the tmux sessions of the focused session's server, read
// again every tmuxEvery while the panel shows, with their windows and the
// terminals attached, and what can be done to them: create, attach in the
// focused terminal or a new tab, detach the focused terminal, rename and
// end.

// tmuxEvery is how often the panel reads the server again; tmuxTimeout
// bounds one tmux command.
const (
	tmuxEvery   = 5 * time.Second
	tmuxTimeout = 15 * time.Second
)

// Where a session the panel creates opens.
const (
	tmuxOpenHere = iota
	tmuxOpenTab
	tmuxOpenNone
)

// tmuxState is what the tmux panel read, of which session and for which
// terminal (tag), and what it does.
type tmuxState struct {
	conn, connects, gen int
	tag                 string
	running             bool
	at                  time.Time
	info                session.TmuxInfo
	read                bool
	err                 error
	// busy is the ID of the session acted on, tmuxBusyAll for an action
	// on none (detaching, creating), "" for none
	busy string
	// open are the sessions whose windows show, by ID
	open map[string]bool
	// confirm asks before the session confirmID ends
	confirm   bool
	confirmID string
	// formOpen shows form, the sheet creating or renaming a session;
	// formSeq counts the openings, for its focus
	formOpen bool
	form     tmuxForm
	formSeq  int
	hold     focusHold
}

const tmuxBusyAll = "*"

// tmuxForm is the sheet creating a session, or renaming the session id.
type tmuxForm struct {
	id        string
	name, dir string
	where     int
	err       string
}

// tmuxErrText is a failure of the panel in words.
func tmuxErrText(m locales.Messages, err error) string {
	if errors.Is(err, session.ErrNoTmux) {
		return m.TmuxMissing()
	}
	return m.TmuxFailed(err.Error())
}

// tmuxHere is tc when it is a terminal, the one the panel attaches in.
func tmuxHere(tc *conn) *conn {
	if tc == nil || tc.kind != layout.Terminal || tc.s == nil {
		return nil
	}
	return tc
}

// tmuxTag is the mark of here's shell, "" when it has none.
func tmuxTag(here *conn) string {
	if here == nil {
		return ""
	}
	if tag, ok := here.s.Tag(); ok {
		return tag
	}
	return ""
}

// readTmux lists tc's tmux sessions when it is time.
func (a *App) readTmux(c *ui.Context, tc *conn, tag string, now bool) {
	d := a.tmux
	connects := tc.s.Status().Connects
	if d.conn != tc.id || d.connects != connects || d.tag != tag {
		d.gen++
		d.conn, d.connects, d.tag, d.running, d.at = tc.id, connects, tag, false, time.Time{}
		d.info, d.read, d.err, d.busy = session.TmuxInfo{}, false, nil, ""
	}
	if d.running {
		return
	}
	if wait := tmuxEvery - c.Now().Sub(d.at); !now && !d.at.IsZero() && wait > 0 {
		c.After(wait)
		return
	}
	if !now && errors.Is(d.err, session.ErrNoTmux) {
		return // no use asking again; the refresh button does
	}
	client := tc.s.Client()
	if client == nil {
		return
	}
	d.running, d.at = true, c.Now()
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
		info, err := session.TmuxList(ctx, client, tag)
		cancel()
		a.post(func() {
			if d.gen != gen {
				return
			}
			d.running, d.err = false, err
			if err == nil {
				d.info, d.read = info, true
				if tag != "" && a.conns[tc.id] == tc {
					a.tmuxSeen(tc, info, time.Now())
				}
			}
		})
	}()
}

// tmuxRun runs fn on tc's server as busy, then reads the sessions again:
// done follows on the UI thread when it worked, a toast when it did not.
func (a *App) tmuxRun(tc *conn, busy string, fn func(context.Context, *ssh.Client) error, done func()) {
	d := a.tmux
	client := tc.s.Client()
	if client == nil || d.busy != "" {
		return
	}
	d.busy = busy
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
		err := fn(ctx, client)
		cancel()
		a.post(func() {
			if d.gen == gen {
				d.busy, d.at = "", time.Time{} // read again at once
			}
			if err != nil {
				a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
					return ui.Toast{Title: tmuxErrText(m, err), Type: "error"}
				})
				return
			}
			if done != nil {
				done()
			}
		})
	}()
}

// tmuxAttach shows the session id, window first (-1 its current), in the
// focused terminal: by switching its tmux client when it is attached to
// a session, by typing the attach command when its shell waits at its
// prompt, and else in a new tab, saying why.
func (a *App) tmuxAttach(tc *conn, id string, window int) {
	here := tmuxHere(tc)
	if here == nil {
		a.tmuxOpenTab(tc, id, window)
		return
	}
	mine, inTmux := a.tmux.info.Mine()
	tag := tmuxTag(here)
	// How it went: typed, or a new tab for a terminal running a program
	// (busy) or of which the server could not tell (unknown).
	var typed, busy, unknown bool
	a.tmuxRun(tc, id, func(ctx context.Context, client *ssh.Client) error {
		if inTmux {
			return session.TmuxSwitch(ctx, client, mine.TTY, id, window)
		}
		idle, err := session.TmuxShellIdle(ctx, client, tag)
		switch {
		case errors.Is(err, session.ErrTmuxUnknown):
			unknown = true
			return nil
		case err != nil:
			return err
		case !idle:
			busy = true
			return nil
		}
		typed = true
		return session.TmuxSelectWindow(ctx, client, id, window)
	}, func() {
		if busy || unknown {
			a.tmuxOpenTab(tc, id, window)
			a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
				if busy {
					return ui.Toast{Title: m.TmuxBusyNewTab()}
				}
				return ui.Toast{Title: m.TmuxUnknownNewTab()}
			})
			return
		}
		if typed {
			if here.s.Status().State != session.Connected {
				return
			}
			here.s.Write([]byte(session.TmuxAttachCommand(id) + "\r"))
		}
		if here.start != nil {
			here.start.setMark(a.tmuxMarkOf(id))
		}
	})
}

// tmuxMarkOf is the mark of the session id, as the panel read it.
func (a *App) tmuxMarkOf(id string) *tmuxMark {
	if s, ok := a.tmux.info.Session(id); ok {
		return markOf(s, time.Now())
	}
	return &tmuxMark{id: id, at: time.Now()}
}

// tmuxOpenTab opens a terminal on tc's host in a new tab, which attaches
// to the session id, at window (-1 its current), once it is connected,
// and again after a reconnect.
func (a *App) tmuxOpenTab(tc *conn, id string, window int) {
	if session.TmuxAttachCommand(id) == "" {
		return
	}
	mark := a.tmuxMarkOf(id)
	open := func() {
		a.openTerminal(tc.host.ID, func(cn *conn) {
			if cn.start != nil {
				cn.start.mark = mark
			}
		})
	}
	if window < 0 {
		open()
		return
	}
	a.tmuxRun(tc, id, func(ctx context.Context, client *ssh.Client) error {
		return session.TmuxSelectWindow(ctx, client, id, window)
	}, open)
}

// tmuxFreeName is the first of main, main-2, … no session has.
func tmuxFreeName(ss []session.TmuxSession) string {
	taken := map[string]bool{}
	for _, s := range ss {
		taken[s.Name] = true
	}
	name := "main"
	for i := 2; taken[name]; i++ {
		name = "main-" + strconv.Itoa(i)
	}
	return name
}

// openTmuxForm opens the sheet creating a session (s nil) or renaming s.
func (a *App) openTmuxForm(tc *conn, s *session.TmuxSession) {
	d := a.tmux
	d.formSeq++
	d.formOpen = true
	if s != nil {
		d.form = tmuxForm{id: s.ID, name: s.Name}
		return
	}
	f := tmuxForm{name: tmuxFreeName(d.info.Sessions), dir: tc.host.TerminalDir, where: tmuxOpenHere}
	if here := tmuxHere(tc); here == nil {
		f.where = tmuxOpenTab
	} else if here.cwd != "" {
		f.dir = here.cwd
	}
	d.form = f
}

// submitTmuxForm creates or renames the session, when its name will do.
func (a *App) submitTmuxForm(m locales.Messages, tc *conn) {
	d := a.tmux
	f := &d.form
	name := strings.TrimSpace(f.name)
	if !session.TmuxNameOK(name) {
		f.err = m.TmuxNameInvalid()
		return
	}
	for _, s := range d.info.Sessions {
		if s.Name == name && s.ID != f.id {
			f.err = m.TmuxNameTaken(name)
			return
		}
	}
	d.formOpen = false
	if f.id != "" {
		id := f.id
		a.tmuxRun(tc, id, func(ctx context.Context, client *ssh.Client) error {
			return session.TmuxRename(ctx, client, id, name)
		}, nil)
		return
	}
	var id string
	dir, where := strings.TrimSpace(f.dir), f.where
	a.tmuxRun(tc, tmuxBusyAll, func(ctx context.Context, client *ssh.Client) (err error) {
		id, err = session.TmuxNew(ctx, client, name, dir)
		return err
	}, func() {
		switch where {
		case tmuxOpenHere:
			a.tmuxAttach(tc, id, -1)
		case tmuxOpenTab:
			a.tmuxOpenTab(tc, id, -1)
		}
	})
}

// tmuxPanel is the tmux panel's body.
func (a *App) tmuxPanel(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	if a.tmux == nil {
		a.tmux = &tmuxState{open: map[string]bool{}}
	}
	d := a.tmux
	cn := a.focused()
	tc := a.sshConn(cn)
	if note := a.usageNote(m, cn, tc); note != "" {
		panelNote(c, tok, note)
		return
	}
	here := tmuxHere(tc)
	a.readTmux(c, tc, tmuxTag(here), false)
	ui.Row(c.Key("tmuxtools")).Gap(8).FillWidth().AlignItems(ui.Center).Children(func() {
		summary := ""
		if d.read {
			summary = m.TmuxSessionCount(len(d.info.Sessions))
			if d.info.Version != "" {
				summary = m.TmuxSummary(d.info.Version, summary)
			}
		}
		ui.Text(c, summary).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine().Grow(1).Shrink(1)
		if d.running || d.busy == tmuxBusyAll {
			ui.Spinner(c)
		}
		if (IconButton{Icon: icons.Refresh, Label: m.Refresh(), Disabled: d.running}).Clicked(c, "tmuxrefresh") {
			d.err = nil
			a.readTmux(c, tc, d.tag, true)
		}
		if (IconButton{Icon: icons.Add, Label: m.TmuxNewSession(), Disabled: !d.read || d.busy != ""}).Clicked(c, "tmuxnew") {
			a.openTmuxForm(tc, nil)
		}
	})
	switch {
	case d.err != nil:
		ui.Row(c.Key("tmuxerr")).FillWidth().Padding(8, 10).Gap(8).Radius(theme.RadiusControl).
			Background(tok.Warning.Alpha(0.12)).Border(1, tok.Warning.Alpha(0.3)).Children(func() {
			ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(tok.Warning)
			ui.Text(c, a.pv(tmuxErrText(m, d.err))).FontSize(theme.Footnote).Shrink(1)
		})
		if !d.read {
			return
		}
	case !d.read:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			panelNote(c, tok, m.MonitorWaiting())
		})
		return
	}
	mine, inTmux := d.info.Mine()
	if inTmux {
		if s, ok := d.info.Session(mine.Session); ok {
			ui.Row(c.Key("tmuxmine")).FillWidth().Padding(6, 6, 6, 10).Gap(8).AlignItems(ui.Center).Radius(theme.RadiusControl).
				Background(tok.Accent.Alpha(0.12)).Border(1, tok.Accent.Alpha(0.3)).Children(func() {
				ui.Icon(c, icons.TmuxOn.SVG).Size(14, 14).Shrink(0).TextColor(tok.Accent)
				ui.Text(c, m.TmuxHereBanner(a.pv(s.Name))).FontSize(theme.Footnote).Grow(1).Shrink(1)
				if (IconButton{Icon: icons.Detach, Label: m.TmuxDetach(), Disabled: d.busy != "", Size: Small}).Clicked(c, "tmuxdetach") {
					tty := mine.TTY
					a.tmuxRun(tc, tmuxBusyAll, func(ctx context.Context, client *ssh.Client) error {
						return session.TmuxDetach(ctx, client, tty)
					}, func() {
						if here.start != nil {
							here.start.setMark(nil)
						}
					})
				}
			})
		}
	}
	if len(d.info.Sessions) == 0 {
		panelNote(c, tok, m.TmuxNone())
	} else {
		ui.Scroll(c.Key("tmuxlist")).Grow(1).Shrink(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(theme.Space(2)).Children(func() {
				for _, s := range d.info.Sessions {
					a.tmuxCard(c, m, tok, tc, s, inTmux && mine.Session == s.ID)
				}
			})
		})
	}
	a.tmuxFormView(c, m, tok, tc)
	a.tmuxConfirm(c, m, tc)
}

// tmuxCard is a session: its name, windows, clients and folder, its
// windows when opened, and what can be done to it.
func (a *App) tmuxCard(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn, s session.TmuxSession, mine bool) {
	d := a.tmux
	busy := d.busy != ""
	open := d.open[s.ID]
	a.contentCard(c.Key("tmux:"+s.ID), tok).FillWidth().Padding(theme.Space(3)).Gap(theme.Space(2)).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			softTile(c, icons.Tmux, tok.Tint(s.Name), 22)
			ui.Column(c).Gap(2).Grow(1).Shrink(1).MinWidth(0).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
					ui.Text(c, a.pv(s.Name)).FontSize(theme.Body).FontWeight(600).SingleLine().Shrink(1)
					if mine {
						ui.Box(c).Padding(1, 7).Radius(theme.Capsule).Background(tok.Accent.Alpha(0.15)).Shrink(0).Children(func() {
							ui.Text(c, m.TmuxThisTerminal()).FontSize(theme.Caption).FontWeight(500).TextColor(tok.Accent).SingleLine()
						})
					}
				})
				state, col := m.TmuxNotAttached(), tok.TextMuted
				if s.Attached > 0 {
					state, col = m.TmuxAttachedCount(s.Attached), tok.Success
				}
				ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
					dot(c, col)
					ui.Text(c, m.TmuxWindowCount(s.Windows)+" · "+state).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
				})
			})
			ic, label := icons.Collapse, m.TmuxShowWindows()
			if open {
				ic, label = icons.Expand, m.TmuxHideWindows()
			}
			if (IconButton{Icon: ic, Label: label, Active: open, Toggle: true, Disabled: len(s.Wins) == 0, Size: Small}).Clicked(c, "wins") {
				d.open[s.ID] = !open
			}
		})
		// Its folder, and how long ago it was last used.
		if s.Path != "" || !s.Activity.IsZero() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				t := ui.Text(c, a.pv(s.Path)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Grow(1).Shrink(1).MinWidth(0)
				if fam := a.termFamily(); fam != "" {
					t.Font(fam)
				}
				if !s.Activity.IsZero() {
					d := c.Now().Sub(s.Activity)
					ui.Text(c, m.TmuxActiveAgo(fmtDur(m, d))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(0)
					tickDur(c, d)
				}
			})
		}
		if open {
			ui.Column(c.Key("wins")).Gap(2).FillWidth().Padding(theme.Space(1), 0).Children(func() {
				for _, w := range s.Wins {
					idx := strconv.Itoa(w.Index)
					ui.Row(c.Key("w" + idx)).Gap(6).AlignItems(ui.Center).FillWidth().Children(func() {
						col := tok.Line
						if w.Active {
							col = tok.Accent
						}
						dot(c, col)
						ui.Text(c, idx+": "+a.pv(w.Name)).FontSize(theme.Footnote).SingleLine().Grow(1).Shrink(1).MinWidth(0)
						ui.Text(c, m.TmuxPaneCount(w.Panes)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(0)
						if (IconButton{Icon: icons.Terminal, Label: m.TmuxAttachWindow(idx), Disabled: busy, Size: Small}).Clicked(c, "attach") {
							a.tmuxAttach(tc, s.ID, w.Index)
						}
					})
				}
			})
		}
		ui.Row(c.Key("acts")).Gap(2).AlignItems(ui.Center).FillWidth().Children(func() {
			if (IconButton{Icon: icons.Terminal, Label: m.TmuxAttachHere(), Disabled: busy || mine || tmuxHere(tc) == nil}).Clicked(c, "here") {
				a.tmuxAttach(tc, s.ID, -1)
			}
			if (IconButton{Icon: icons.OpenInTab, Label: m.TmuxOpenTab(), Disabled: busy}).Clicked(c, "tab") {
				a.tmuxOpenTab(tc, s.ID, -1)
			}
			if (IconButton{Icon: icons.Rename, Label: m.TmuxRename(), Disabled: busy}).Clicked(c, "rename") {
				a.openTmuxForm(tc, &s)
			}
			ui.Spacer(c)
			if d.busy == s.ID {
				ui.Spinner(c)
			}
			if (IconButton{Icon: icons.Delete, Label: m.TmuxKill(), Danger: true, Disabled: busy}).Clicked(c, "kill") {
				d.confirm, d.confirmID = true, s.ID
			}
		})
	})
}

// tmuxFormView is the sheet creating or renaming a session.
func (a *App) tmuxFormView(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	d := a.tmux
	if !d.formOpen {
		return
	}
	f := &d.form
	creating := f.id == ""
	title, ic := m.TmuxRename(), icons.Rename
	if creating {
		title, ic = m.TmuxNewSession(), icons.Tmux
	}
	sheet(stickyModal(c, &d.formOpen, func() {
		ui.Column(c).Gap(18).Width(400).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, ic, theme.Brand, 36)
				sheetTitle(c, title)
			})
			ui.Form(c, func() {
				ui.Field(c, m.TmuxName(), func() {
					in := formTextInput(c.Key("tmuxname"), &f.name).AutoFocus()
					d.hold.apply(c, d.formSeq, in)
					if in.Changed() {
						f.err = ""
					}
					if in.Submitted() {
						a.submitTmuxForm(m, tc)
					}
				})
				if f.err != "" {
					ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
				}
				if creating {
					ui.Field(c, m.TmuxDir(), func() {
						in := formTextInput(c.Key("tmuxdir"), &f.dir).Placeholder(m.TmuxDirPlaceholder())
						if in.Submitted() {
							a.submitTmuxForm(m, tc)
						}
					})
					ui.Field(c, m.TmuxOpenIn(), func() {
						formSegmented(c.Key("tmuxwhere"), &f.where, false, m.TmuxThisTerminal(), m.NewTab(), m.TmuxOpenBackground()).
							Label(m.TmuxOpenIn())
					})
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					d.formOpen = false
				}
				label := m.Save()
				if creating {
					label = m.Create()
				}
				if pill(c, label, true, false, false) {
					a.submitTmuxForm(m, tc)
				}
			})
		})
	}), tok)
}

// tmuxConfirm asks before a session ends, saying so when terminals are
// still attached to it.
func (a *App) tmuxConfirm(c *ui.Context, m locales.Messages, tc *conn) {
	d := a.tmux
	if !d.confirm {
		return
	}
	s, ok := d.info.Session(d.confirmID)
	if !ok {
		d.confirm = false
		return
	}
	msg := m.TmuxKillConfirm(a.pv(s.Name))
	if s.Attached > 0 {
		msg = m.TmuxKillConfirmAttached(a.pv(s.Name))
	}
	if ui.AlertDialog(c, &d.confirm, m.TmuxKill(), msg, m.Cancel(), m.TmuxEnd()) == 1 {
		id := s.ID
		a.tmuxRun(tc, id, func(ctx context.Context, client *ssh.Client) error {
			return session.TmuxKill(ctx, client, id)
		}, nil)
	}
}
