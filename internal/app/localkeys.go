package app

import (
	"errors"
	"path/filepath"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/localkeys"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// promptKeyFor is promptKey for the failure err: a key file of this
// computer that needs its passphrase is asked for under its own key, as
// every host offering it shares the answer.
func (a *App) promptKeyFor(h assets.Host, kind session.Kind, err error) (string, bool) {
	var le *session.LocalKeyError
	if errors.As(err, &le) {
		return localKeyPrompt(le.ID), true
	}
	return a.promptKey(h, kind)
}

// rememberAfterLogin notes whether the passphrase typed in p is to be
// remembered; it is, once it logged in (rememberOnLogin), so that a
// wrong one is never kept.
func (a *App) rememberAfterLogin(p *credPrompt) {
	id := p.localKey.ID
	a.mu.Lock()
	defer a.mu.Unlock()
	if p.remember && p.value != "" {
		a.rememberKeys[id] = localkeys.Remembered{ID: id, Path: p.localKey.Path, Fingerprint: ssh.FingerprintSHA256(p.localKey.Public)}
	} else {
		delete(a.rememberKeys, id)
	}
}

// rememberOnLogin keeps the passphrase typed for the key id, which just
// logged in, when the user asked to remember it. It runs off the UI
// thread.
func (a *App) rememberOnLogin(id string) {
	a.mu.Lock()
	r, ok := a.rememberKeys[id]
	pass := a.typed[localKeyPrompt(id)]
	delete(a.rememberKeys, id)
	a.mu.Unlock()
	if !ok || pass == "" {
		return
	}
	key := keychain.LocalKeyPassphrase(id)
	err := a.kc.Set(key, []byte(pass))
	if err == nil {
		if err = a.lk.Remember(r); err != nil {
			a.kc.Delete(key)
		}
	}
	if err != nil {
		a.post(func() {
			a.toast(func(m locales.Messages) ui.Toast {
				return ui.Toast{Title: m.ErrKeychainSave(secretStoreErrorText(m, err)), Type: "error"}
			})
		})
	}
}

// skipLocalKey passes over the key asked about for this run: the panes
// waiting on it connect again with the other keys, else ask for the
// password.
func (a *App) skipLocalKey() {
	p := a.prompt
	if p == nil || p.localKey == nil {
		return
	}
	a.prompt, a.prompting = nil, false
	a.mu.Lock()
	a.skipKeys[p.localKey.ID] = true
	delete(a.rememberKeys, p.localKey.ID)
	a.mu.Unlock()
	if c := a.conns[p.conn]; c != nil {
		a.retryPrompted(c, p.key)
	}
}

// useRememberedPassphrase unlocks the encrypted store, where the
// passphrase asked for is remembered, and connects again with it.
func (a *App) useRememberedPassphrase() {
	p := a.prompt
	if p == nil || p.localKey == nil {
		return
	}
	a.withSecrets(func() {
		if a.prompt != p {
			return
		}
		a.prompt, a.prompting = nil, false
		if c := a.conns[p.conn]; c != nil {
			a.retryPrompted(c, p.key)
		}
	})
}

// forgetLocalKey drops the remembered passphrase of the key id.
func (a *App) forgetLocalKey(id string) error {
	if err := a.kc.Delete(keychain.LocalKeyPassphrase(id)); err != nil {
		return err
	}
	return a.lk.Forget(id)
}

// localKeysSection: whether hosts asking for their password first try
// the keys of this computer, whether MCP connections may, and the
// passphrases remembered for them.
func (a *App) localKeysSection(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	a.settingsSection(c, tok, icons.Key, m.LocalKeysSection(), func(g *settingsGroup) {
		g.row(m.LocalKeysTry(), m.LocalKeysTryNote(), func() {
			a.lkChoice = s.LocalKeys
			if formSwitch(c.Key("localkeys"), &a.lkChoice).Label(m.LocalKeysTry()).Changed() {
				s.LocalKeys, *changed = a.lkChoice, true
			}
		})
		g.row(m.LocalKeysMCP(), m.LocalKeysMCPNote(), func() {
			a.lkMCPChoice = s.LocalKeysMCP && s.LocalKeys
			if formDisabled(formSwitch(c.Key("localkeys:mcp"), &a.lkMCPChoice).Label(m.LocalKeysMCP()), !s.LocalKeys).Changed() {
				s.LocalKeysMCP, *changed = a.lkMCPChoice, true
			}
		})
		for _, r := range a.lk.Remembered() {
			g.row(m.RememberedKeyPassphrase(filepath.Base(r.Path)), a.homePath(r.Path)+" · "+r.Fingerprint, func() {
				if iconButton(c, "localkey:forget:"+r.ID, icons.Delete, m.ForgetKeyPassphrase()) {
					a.withSecrets(func() {
						if err := a.forgetLocalKey(r.ID); err != nil {
							a.toast(func(m locales.Messages) ui.Toast {
								return ui.Toast{Title: m.ErrKeychainDelete(secretStoreErrorText(m, err)), Type: "error"}
							})
						}
					})
				}
			})
		}
	})
}
