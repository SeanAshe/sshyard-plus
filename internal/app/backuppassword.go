package app

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vault"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The default backup password is set once on the backup page; an export
// or a backup location may then seal with it rather than with a password
// of its own. As with a location's own, only the key derived from it is
// kept, in the encrypted local store (keychain.DefaultBackupKey), and the
// settings record only that it is set.

// Where a backup's password comes from, the index in the sheets'
// segmented control.
const (
	pwDefault = iota // the default backup password
	pwCustom         // its own, typed for it
)

var (
	// errDefaultBackupKey reports a default backup password that is not
	// set, or whose key is not in the store.
	errDefaultBackupKey = errors.New("app: default backup key missing")
	// errDefaultBackupInUse reports removing the default backup password
	// while backup locations seal with it.
	errDefaultBackupInUse = errors.New("app: default backup password in use")
)

// readBackupKey is the key kept under name in the store, missing when
// there is none.
func (a *App) readBackupKey(name string, missing error) (vault.Key, error) {
	b, err := a.kc.Get(name)
	defer clear(b)
	switch {
	case errors.Is(err, keychain.ErrNotFound):
		return vault.Key{}, missing
	case err != nil:
		return vault.Key{}, err
	}
	var k vault.Key
	if json.Unmarshal(b, &k) != nil || !k.Valid() {
		k.Clear()
		return vault.Key{}, missing
	}
	return k, nil
}

// defaultBackupKey is the key derived from the default backup password.
func (a *App) defaultBackupKey() (vault.Key, error) {
	if !a.settings.DefaultBackupPassword {
		return vault.Key{}, errDefaultBackupKey
	}
	return a.readBackupKey(keychain.DefaultBackupKey, errDefaultBackupKey)
}

// passwordSource is the field choosing where a backup's password comes
// from.
func passwordSource(c *ui.Context, m locales.Messages, key string, source *int) {
	ui.Field(c, m.BackupPasswordSource(), func() {
		formSegmented(c.Key(key), source, false, m.DefaultBackupPassword(), m.CustomPassword()).Label(m.BackupPasswordSource())
	})
}

// passwordForm is the sheet setting the default backup password, typed
// twice. The password lives here only while the sheet is open.
type passwordForm struct {
	password, confirm string
	// busy is set while the key is derived, off the UI thread
	busy bool
	err  string
}

func (a *App) openDefaultPassword() {
	a.defaultPw, a.settingDefaultPw = &passwordForm{}, true
}

func (a *App) closeDefaultPassword() {
	a.defaultPw, a.settingDefaultPw = nil, false
}

// SaveDefaultPassword checks the sheet, derives the key off the UI thread,
// then keeps it in place of the one before.
func (a *App) SaveDefaultPassword(m locales.Messages) bool {
	f := a.defaultPw
	if f == nil || f.busy {
		return false
	}
	f.err = ""
	switch {
	case len([]rune(f.password)) < minBackupPassword:
		f.err = m.BackupPasswordShort(strconv.Itoa(minBackupPassword))
		return false
	case f.password != f.confirm:
		f.err = m.BackupPasswordMismatch()
		return false
	}
	if !a.unlocked() {
		a.withSecrets(func() {
			if a.defaultPw == f {
				a.SaveDefaultPassword(m)
			}
		})
		return false
	}
	pw, cost := []byte(f.password), a.kdf
	f.busy = true
	go func() {
		k, err := vault.Derive(pw, cost)
		clear(pw)
		a.post(func() {
			defer k.Clear()
			f.busy = false
			switch {
			case a.defaultPw != f:
			case err != nil:
				f.err = m.ErrGeneric(err.Error())
			default:
				if f.err = a.commitDefaultKey(m, k); f.err == "" {
					a.closeDefaultPassword()
				}
			}
		})
	}()
	return true
}

// commitDefaultKey keeps k as the default backup password's key, then
// records that one is set in the settings, which failing puts the store
// back. It says why it could not.
func (a *App) commitDefaultKey(m locales.Messages, k vault.Key) string {
	b, err := json.Marshal(k)
	if err != nil {
		return m.ErrGeneric(err.Error())
	}
	defer clear(b)
	tx := &secretTx{kc: a.kc}
	fail := func(msg string) string {
		if rerr := tx.rollback(); rerr != nil {
			msg = m.ErrKeychainRestore(msg, secretStoreErrorText(m, rerr))
		}
		return msg
	}
	if err := tx.setBatch([]keychain.Change{{Key: keychain.DefaultBackupKey, Value: b}}); err != nil {
		return fail(m.ErrKeychainSave(secretStoreErrorText(m, err)))
	}
	s := a.settings
	s.DefaultBackupPassword = true
	if err := a.ApplySettings(s); err != nil {
		return fail(m.ErrGeneric(err.Error()))
	}
	return ""
}

// RemoveDefaultPassword forgets the default backup password, unless a
// backup location seals with it. The backups sealed with it still open
// with it.
func (a *App) RemoveDefaultPassword() error {
	if !a.settings.DefaultBackupPassword {
		return nil
	}
	if slices.ContainsFunc(a.settings.BackupTargets, func(t settings.BackupTarget) bool { return t.DefaultPassword }) {
		return errDefaultBackupInUse
	}
	if err := a.kc.Delete(keychain.DefaultBackupKey); err != nil {
		return &keychainDeleteError{err}
	}
	s := a.settings
	s.DefaultBackupPassword = false
	return a.ApplySettings(s)
}

// defaultPasswordSection is the backup page's default backup password.
func (a *App) defaultPasswordSection(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	a.settingsSection(c, tok, nil, "", func(g *settingsGroup) {
		set := a.settings.DefaultBackupPassword
		note, label := m.DefaultBackupPasswordNote(), m.SetPasswordEllipsis()
		if set {
			note, label = m.DefaultBackupPasswordSetNote(), m.ChangePasswordEllipsis()
		}
		g.row(m.DefaultBackupPassword(), note, func() {
			if set && pill(c, m.Remove(), false, true, false) {
				a.runSecretAction(a.RemoveDefaultPassword, func(err error) {
					if errors.Is(err, errDefaultBackupInUse) {
						a.toastError(m.ErrDefaultBackupPasswordInUse())
						return
					}
					a.toastError(a.inUseText(m, err))
				})
			}
			if pill(c, label, false, false, false) {
				a.openDefaultPassword()
			}
		})
	})
}

// defaultPasswordView is the sheet setting the default backup password.
func (a *App) defaultPasswordView(c *ui.Context, m locales.Messages) {
	f := a.defaultPw
	if f == nil {
		return
	}
	tok := currentTokens(c)
	sheet(stickyModal(c, &a.settingDefaultPw, func() {
		ui.Column(c).Gap(18).Width(420).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, icons.Password, tok.TintAt(6), 36)
				sheetTitle(c, m.DefaultBackupPassword())
			})
			ui.Form(c, func() {
				ui.Field(c, m.BackupPassword(), func() {
					in := formTextInput(c.Key("dp:pw"), &f.password).Password().AutoFocus().Disabled(f.busy)
					a.defaultPwHold.apply(c, f, in)
				})
				ui.Field(c, m.ConfirmPassword(), func() {
					if formTextInput(c.Key("dp:confirm"), &f.confirm).Password().Disabled(f.busy).Submitted() {
						a.SaveDefaultPassword(m)
					}
				})
			})
			ui.Text(c, m.DefaultBackupPasswordSheetNote()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			if f.err != "" {
				ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.settingDefaultPw = false
				}
				if pill(c, m.Save(), true, false, f.busy) {
					a.SaveDefaultPassword(m)
				}
			})
		})
	}), tok)
	if !a.settingDefaultPw && a.defaultPw == f {
		a.closeDefaultPassword()
	}
}
