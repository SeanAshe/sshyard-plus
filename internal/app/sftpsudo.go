package app

import (
	"errors"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/sftpsvc"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// sudoAsk is the sheet asking the sudo password of the host of a files
// pane whose SFTP runs through sudo (Host.SFTPSudo); refused says sudo
// refused the password it was given.
type sudoAsk struct {
	conn    int
	host    assets.Host
	value   string
	refused bool
}

// sudoKey is where the sudo password typed for the host of id goes in
// a.typed: in memory, for this run only.
func sudoKey(id string) string { return "sudo/" + id }

// sudoPassword answers sudo for the files panes of h: the password typed
// for it this run, else the one h logs in with, typed or saved; "" when
// none is known. typed says it is the one typed for sudo. It runs off the
// UI thread, and a locked store is not unlocked for it.
func (a *App) sudoPassword(h assets.Host) (pass string, typed bool) {
	a.mu.Lock()
	pass = a.typed[sudoKey(h.ID)]
	a.mu.Unlock()
	if pass != "" {
		return pass, true
	}
	key, passphrase := a.promptKey(h, session.KindAuth)
	if key == "" || passphrase {
		return "", false
	}
	a.mu.Lock()
	pass = a.typed[key]
	a.mu.Unlock()
	if pass != "" || h.Auth == assets.AuthAsk {
		return pass, false
	}
	b, err := a.kc.Get(key)
	if err != nil {
		return "", false
	}
	return string(b), false
}

// sudoFailed holds the pane's SFTP after sudo failed with err, so that
// it is not tried again with the same answer until the user acts; a
// password sudo wants opens the sheet asking it. tried says a password
// was given, typed that it was the one typed for sudo, now forgotten.
func (a *App) sudoFailed(cn *conn, h assets.Host, err error, tried, typed bool) {
	s := &cn.fs.remote
	s.sudoHeld = true
	m := locales.In(a.settings.Language)
	if !errors.Is(err, sftpsvc.ErrSudoPassword) {
		s.err = sudoErrText(m, err)
		return
	}
	if typed {
		a.mu.Lock()
		delete(a.typed, sudoKey(h.ID))
		a.mu.Unlock()
	}
	s.err = m.SudoPasswordNeeded()
	if tried {
		s.err = m.SudoPasswordRefused()
	}
	if a.sudo == nil {
		a.sudo, a.sudoAsking = &sudoAsk{conn: cn.id, host: h, refused: tried}, true
	}
}

// sudoErrText says why sudo did not start the SFTP server: what sudo
// said, when it said something.
func sudoErrText(m locales.Messages, err error) string {
	reason := err.Error()
	if errors.Is(err, sftpsvc.ErrSudoNoServer) {
		reason = m.SudoNoServer()
	} else if _, said, ok := strings.Cut(reason, sftpsvc.ErrSudo.Error()+": "); ok {
		reason = said
	}
	return m.SudoFailed(reason)
}

// retrySudo opens SFTP again in the files panes of the host of id held
// after sudo failed.
func (a *App) retrySudo(id string) {
	for _, cn := range a.conns {
		if cn.fs == nil || cn.host.ID != id || !cn.fs.remote.sudoHeld {
			continue
		}
		cn.fs.remote.sudoHeld, cn.fs.remote.err, cn.fs.remote.listErr = false, "", false
		a.ensureSFTP(cn)
	}
}

// submitSudo keeps the password typed in p for this run, then opens SFTP
// again in the panes waiting for it.
func (a *App) submitSudo(p *sudoAsk) {
	if p.value == "" {
		return
	}
	a.mu.Lock()
	a.typed[sudoKey(p.host.ID)] = p.value
	a.mu.Unlock()
	a.sudo, a.sudoAsking = nil, false
	a.retrySudo(p.host.ID)
}

// askSudo opens the sheet asking the sudo password of the pane's host.
func (a *App) askSudo(cn *conn) {
	if a.sudo == nil {
		a.sudo = &sudoAsk{conn: cn.id, host: a.liveHost(cn.host)}
	}
	a.sudoAsking = true
}

// sudoHeldActions are the ways on from a pane whose sudo failed: typing
// the password, or trying again.
func (a *App) sudoHeldActions(c *ui.Context, cn *conn, m locales.Messages) {
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		if (IconButton{Icon: icons.Key, Label: m.SudoEnterPassword()}).Clicked(c, "sudo-ask") {
			a.askSudo(cn)
		}
		if (IconButton{Icon: icons.Reconnect, Label: m.SudoRetry()}).Clicked(c, "sudo-retry") {
			a.retrySudo(cn.host.ID)
		}
	})
}

// sudoPromptView is the sheet asking a sudo password.
func (a *App) sudoPromptView(c *ui.Context, m locales.Messages) {
	p := a.sudo
	if p == nil {
		return
	}
	tok := currentTokens(c)
	title := m.SudoPromptTitle(a.pv(p.host.Target()))
	sheet(ui.Modal(c, &a.sudoAsking, func() {
		ui.Column(c).Gap(14).Width(380).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				hostAvatar(c, tok, a.liveHost(p.host), 36)
				ui.Text(c, title).FontSize(theme.Title3).FontWeight(600).Shrink(1)
			})
			if p.refused {
				ui.Text(c, m.SudoPasswordRefused()).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			in := formTextInput(c.Key("sudo-secret"), &p.value).Password().AutoFocus().Label(title).FillWidth()
			a.sudoHold.apply(c, p, in)
			ui.Text(c, m.TypedNotSaved()).FontSize(theme.Caption).TextColor(tok.TextMuted)
			submit := in.Submitted()
			ui.Row(c).Gap(8).Margin(4, 0, 0, 0).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.sudoAsking = false
				}
				if pill(c, m.SudoSubmit(), true, false, p.value == "") {
					submit = true
				}
			})
			if submit {
				a.submitSudo(p)
			}
		})
	}), tok)
	if !a.sudoAsking {
		a.sudo = nil
	}
}
