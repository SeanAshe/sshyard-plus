package app

import (
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// hostCardDelay is how long the pointer rests on a sidebar host before
// its card shows.
const hostCardDelay = time.Second

// cardHover is the sidebar item the pointer rests on, by its key, since
// when; hidden is set once a press, a drag or a menu hid its card, until
// the pointer leaves the item.
type cardHover struct {
	key    string
	since  time.Time
	hidden bool
}

// hostCardOn shows the card of h beside el, its sidebar item of key, once
// the pointer has rested on it for hostCardDelay, until it leaves.
func (a *App) hostCardOn(c *ui.Context, m locales.Messages, tok theme.Tokens, el ui.Element, key string, h assets.Host) {
	if !el.Hovered() {
		if a.card.key == key {
			a.card = cardHover{}
		}
		return
	}
	now := c.Now()
	if a.card.key != key {
		a.card = cardHover{key: key, since: now}
	}
	if el.Pressed() || el.Dragging() || a.menu.key != "" {
		a.card.hidden = true
	}
	if a.card.hidden {
		return
	}
	if wait := hostCardDelay - now.Sub(a.card.since); wait > 0 {
		c.After(wait)
		return
	}
	a.hostCard(c, m, tok, el, h)
}

// hostCard is a host's card, beside anchor: who it is, how it is reached
// and how it is, then its provider, subscription and note. The pointer
// goes through it.
func (a *App) hostCard(c *ui.Context, m locales.Messages, tok theme.Tokens, anchor ui.Element, h assets.Host) {
	now := c.Now()
	ui.Overlay(c, func() {
		card := ui.Column(c.Key("hostcard")).AttachTo(anchor, ui.AnchorTopEnd, ui.AnchorTopStart).MarginStart(theme.Space(2)).
			Width(300).Padding(theme.Space(3)).Gap(theme.Space(2.5)).Radius(theme.RadiusCard).
			Background(tok.Inset).Border(1, tok.GlassEdge).PassThrough().Role(ui.RoleTooltip)
		tok.Lift.On(card)
		card.Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
				hostAvatar(c, tok, h, 32)
				ui.Column(c).Gap(1).Grow(1).Shrink(1).MinWidth(0).Children(func() {
					ui.Text(c, a.pv(h.Label())).FontSize(theme.Body).FontWeight(600).SingleLine()
					ui.Text(c, a.pv(h.Target())).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
				})
			})
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			fact := func(label, value string, col ui.Color, lines int) {
				ui.Row(c).Gap(theme.Space(3)).FillWidth().AlignItems(ui.Start).Children(func() {
					ui.Text(c, label).FontSize(theme.Caption).TextColor(tok.TextMuted).Width(76).Shrink(0).SingleLine()
					t := ui.Text(c, value).FontSize(theme.Footnote).Grow(1).Shrink(1).MinWidth(0).MaxLines(lines)
					if col != (ui.Color{}) {
						t.TextColor(col)
					}
				})
			}
			none := ui.Color{}
			if h.Group != "" {
				fact(m.FieldGroup(), h.Group, none, 1)
			}
			fact(m.FieldAuth(), a.authText(m, h), none, 1)
			if px, ok := a.proxyOf(h); ok {
				fact(m.FieldProxy(), px.Name, none, 1)
			}
			if path, _ := a.store.JumpPath(h.ID); len(path) > 0 {
				var jumps []string
				for _, j := range path {
					jumps = append(jumps, a.pv(j.Label()))
				}
				fact(m.JumpHosts(), strings.Join(jumps, " → "), none, 2)
			}
			if st, ok := a.hostState(h.ID); ok {
				fact(m.FieldStatus(), a.pv(a.statusText(m, h, session.Status{State: st}, now)), stateColor(tok, st), 1)
			}
			if r, ok := a.pingOf(h); ok && r.ok {
				fact(m.SysLatency(), latencyValue(m, r.d, true), latencyColor(tok, r.d, true), 1)
			}
			if !h.LastUsed.IsZero() {
				fact(m.LastConnected(), h.LastUsed.Local().Format("2006-01-02 15:04"), none, 1)
			}
			if h.Provider != "" {
				fact(m.FieldProvider(), h.Provider, none, 1)
			}
			if line, col, ok := renewalLine(m, tok, h.Renewal, now); ok {
				fact(m.RenewalNext(), line, col, 2)
			}
			if h.Note != "" {
				fact(m.FieldNote(), a.pv(h.Note), none, 4)
			}
		})
	})
}
