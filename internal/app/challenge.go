package app

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// challengeAsk is a round of a server's keyboard-interactive login
// questions (a one-time password, a code sent by text message) waiting
// for the user. The answers are typed into it and handed to the login,
// never kept anywhere else: not in a.typed, the store or a log.
type challengeAsk struct {
	// seq keys its fields apart from an earlier round's, whose typing
	// they must not show
	seq     int
	conn    *conn
	ch      session.Challenge
	answers []string
	// ctx ends when the login stops waiting: the pane closed, the
	// handshake failed, or the user took too long.
	ctx   context.Context
	reply chan challengeReply
}

type challengeReply struct {
	answers []string
	err     error
}

// challengeAsker asks the login questions servers put to c's connection
// in a sheet; nil, failing such logins, for a connection no one watches:
// none (MCP), or one started unattended.
func (a *App) challengeAsker(c *conn) session.Ask {
	if c == nil || c.unattended {
		return nil
	}
	return func(ctx context.Context, ch session.Challenge) ([]string, error) {
		r := &challengeAsk{conn: c, ch: ch, answers: make([]string, len(ch.Questions)), ctx: ctx, reply: make(chan challengeReply, 1)}
		a.post(func() { a.showChallenge(r) })
		select {
		case rep := <-r.reply:
			return rep.answers, rep.err
		case <-ctx.Done():
			a.post(func() { a.dropChallenge(r) })
			return nil, ctx.Err()
		}
	}
}

// showChallenge opens the sheet for r, on the UI thread, unless no one
// can answer it: its pane closed, or was restored at launch and not
// connected by the user since.
func (a *App) showChallenge(r *challengeAsk) {
	switch {
	case r.ctx.Err() != nil:
		return
	case r.conn.id != 0 && a.conns[r.conn.id] != r.conn:
		r.reply <- challengeReply{err: session.ErrChallengeCanceled}
		return
	case r.conn.restoredQuiet:
		r.reply <- challengeReply{err: session.ErrChallengeUnattended}
		return
	}
	a.challengeSeq++
	r.seq = a.challengeSeq
	a.challenges = append(a.challenges, r)
	a.challenging = true
	a.invalidate()
}

// answerChallenge hands the answers typed in r to its login, or, with
// cancel, fails the login as canceled, and closes r's sheet.
func (a *App) answerChallenge(r *challengeAsk, cancel bool) {
	if !a.removeChallenge(r) {
		return
	}
	rep := challengeReply{answers: slices.Clone(r.answers)}
	if cancel {
		rep = challengeReply{err: session.ErrChallengeCanceled}
	}
	clear(r.answers)
	r.reply <- rep
}

// dropChallenge closes r's sheet, the login no longer waiting for it.
func (a *App) dropChallenge(r *challengeAsk) {
	if a.removeChallenge(r) {
		clear(r.answers)
	}
}

// removeChallenge takes r out of the sheet's queue; false when it was not
// in it.
func (a *App) removeChallenge(r *challengeAsk) bool {
	i := slices.Index(a.challenges, r)
	if i < 0 {
		return false
	}
	a.challenges = slices.Delete(a.challenges, i, i+1)
	a.challenging = len(a.challenges) > 0
	a.invalidate()
	return true
}

// challengeView is the sheet asking the first round of login questions
// waiting: who asks, what the server says, and a field for each
// question, hidden unless the server lets its answer show.
func (a *App) challengeView(c *ui.Context, m locales.Messages) {
	if len(a.challenges) == 0 {
		return
	}
	r := a.challenges[0]
	if r.ctx.Err() != nil {
		a.dropChallenge(r)
		return
	}
	tok := currentTokens(c)
	target := r.ch.Hop
	if r.ch.User != "" {
		target = r.ch.User + "@" + target
	}
	title := m.ChallengeTitle(a.pv(target))
	submit := false
	sheet(stickyModal(c, &a.challenging, func() {
		ui.Column(c).Gap(14).Width(400).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				hostAvatar(c, tok, a.liveHost(r.conn.host), 36)
				ui.Text(c, title).FontSize(theme.Title3).FontWeight(600).Shrink(1)
			})
			if s := strings.TrimSpace(r.ch.Name); s != "" {
				ui.Text(c, s).FontSize(theme.Footnote).FontWeight(600)
			}
			if s := strings.TrimSpace(r.ch.Instruction); s != "" {
				ui.Text(c, s).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			}
			var ins []ui.Element
			for i, q := range r.ch.Questions {
				q = strings.TrimSpace(q)
				ui.Column(c).Gap(6).FillWidth().Children(func() {
					ui.Text(c, q).FontSize(theme.Footnote)
					in := formTextInput(c.Key("challenge:"+strconv.Itoa(r.seq)+":"+strconv.Itoa(i)), &r.answers[i]).Label(m.ChallengeAnswer(q)).FillWidth()
					if i >= len(r.ch.Echo) || !r.ch.Echo[i] {
						in.Password()
					}
					if i == 0 {
						in.AutoFocus()
						a.challengeHold.apply(c, r, in)
					}
					ins = append(ins, in)
				})
			}
			for i, in := range ins {
				if in.Submitted() {
					if i+1 < len(ins) {
						ins[i+1].Focus()
					} else {
						submit = true
					}
				}
			}
			ui.Text(c, m.ChallengeHint()).FontSize(theme.Caption).TextColor(tok.TextMuted)
			ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.challenging = false
				}
				if pill(c, m.ChallengeSubmit(), true, false, false) {
					submit = true
				}
			})
		})
	}), tok)
	switch {
	case submit:
		a.answerChallenge(r, false)
	case !a.challenging:
		a.answerChallenge(r, true)
	}
}
