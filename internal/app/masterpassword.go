package app

import (
	"errors"
	"strconv"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The local master password stands in for a system keychain SSHYard
// cannot use (keychain.SetupPassword): it is set up from the unlock
// failure while nothing is stored yet, then every explicit unlock asks for
// it. The about page changes it or moves the master key back to the
// system keychain. The password lives only in the sheet while it is open.

// What the local master password sheet does.
type masterPwMode int

const (
	masterPwUnlock masterPwMode = iota // unlock the store with it
	masterPwSetup                      // protect a new store with it
	masterPwChange                     // replace it
	masterPwSystem                     // move the master key to the system keychain
)

type masterPwForm struct {
	mode                       masterPwMode
	current, password, confirm string
	// busy is set while the key is derived, off the UI thread
	busy bool
	err  string
}

// unlocks reports whether the sheet's action unlocks the store, and so
// stands for the unlock the pending action waits on.
func (f *masterPwForm) unlocks() bool { return f.mode == masterPwUnlock || f.mode == masterPwSetup }

func (a *App) openMasterPassword(mode masterPwMode) {
	if a.masterPwOpen && a.masterPw != nil && a.masterPw.mode == mode {
		return
	}
	a.masterPw, a.masterPwOpen = &masterPwForm{mode: mode}, true
}

func (a *App) closeMasterPassword() {
	a.masterPw, a.masterPwOpen = nil, false
}

// SubmitMasterPassword checks the sheet, then runs its action off the UI
// thread, since deriving the key takes a moment.
func (a *App) SubmitMasterPassword(m locales.Messages) bool {
	f := a.masterPw
	if f == nil || f.busy {
		return false
	}
	f.err = ""
	switch f.mode {
	case masterPwUnlock:
		if f.password == "" {
			return false
		}
	case masterPwSystem:
		if f.current == "" {
			return false
		}
	default:
		switch {
		case f.mode == masterPwChange && f.current == "":
			return false
		case len([]rune(f.password)) < minBackupPassword:
			f.err = m.BackupPasswordShort(strconv.Itoa(minBackupPassword))
			return false
		case f.password != f.confirm:
			f.err = m.BackupPasswordMismatch()
			return false
		}
	}
	kc := a.kc
	current, password := []byte(f.current), []byte(f.password)
	mode := f.mode
	run := func() error {
		switch mode {
		case masterPwUnlock:
			return kc.UnlockPassword(password)
		case masterPwSetup:
			return kc.SetupPassword(password)
		case masterPwChange:
			return kc.ChangePassword(current, password)
		default:
			return kc.UseSystem(current)
		}
	}
	f.busy = true
	if !f.unlocks() {
		go func() {
			err := run()
			clear(current)
			clear(password)
			a.post(func() { a.masterPasswordDone(m, f, err) })
		}()
		return true
	}
	a.unlock.mu.Lock()
	a.unlock.busy, a.unlock.completed = true, false
	a.unlock.mu.Unlock()
	go func() {
		err := run()
		clear(current)
		clear(password)
		a.unlock.mu.Lock()
		defer a.unlock.mu.Unlock()
		if a.unlock.closed {
			a.closeSecrets(kc)
			return
		}
		a.unlock.completed = true
		a.post(func() { a.masterPasswordDone(m, f, err) })
	}()
	return true
}

// masterPasswordDone ends the sheet's action: a wrong password stays in the
// sheet; an unlock's other outcomes go where the system keychain's do.
func (a *App) masterPasswordDone(m locales.Messages, f *masterPwForm, err error) {
	f.busy = false
	wrong := errors.Is(err, keychain.ErrWrongPassword)
	if f.unlocks() {
		if wrong {
			a.unlock.mu.Lock()
			a.unlock.busy = false
			a.unlock.mu.Unlock()
			f.err = m.MasterPasswordWrong()
			return
		}
		if a.masterPw == f {
			a.closeMasterPassword()
		}
		a.acceptUnlock(err)
		return
	}
	switch {
	case a.masterPw != f:
	case wrong:
		f.err = m.MasterPasswordWrong()
	case err != nil:
		f.err = secretStoreErrorText(m, err)
	default:
		a.closeMasterPassword()
	}
}

// masterPasswordView is the local master password sheet.
func (a *App) masterPasswordView(c *ui.Context, m locales.Messages) {
	f := a.masterPw
	if f == nil {
		return
	}
	tok := currentTokens(c)
	title, note, action := m.MasterPasswordUnlockTitle(), m.MasterPasswordUnlockNote(), m.MasterPasswordUnlock()
	switch f.mode {
	case masterPwSetup:
		title, note, action = m.MasterPasswordSetupTitle(), m.MasterPasswordSetupNote(), m.Save()
	case masterPwChange:
		title, note, action = m.MasterPasswordChangeTitle(), "", m.Save()
	case masterPwSystem:
		title, note, action = m.MasterPasswordSystemTitle(), m.MasterPasswordSystemNote(), m.MasterPasswordSystemTitle()
	}
	sheet(stickyModal(c, &a.masterPwOpen, func() {
		ui.Column(c).Gap(18).Width(420).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, icons.Password, tok.TintAt(6), 36)
				sheetTitle(c, title)
			})
			// Each field submits the sheet; the first one holds the focus.
			first := true
			field := func(key, label string, value *string) {
				ui.Field(c, label, func() {
					in := formTextInput(c.Key(key), value).Password().Disabled(f.busy)
					if first {
						in.AutoFocus()
						a.masterPwHold.apply(c, f, in)
						first = false
					}
					if in.Submitted() {
						a.SubmitMasterPassword(m)
					}
				})
			}
			ui.Form(c, func() {
				switch f.mode {
				case masterPwUnlock:
					field("mp:pw", m.MasterPassword(), &f.password)
				case masterPwSetup:
					field("mp:pw", m.MasterPassword(), &f.password)
					field("mp:confirm", m.ConfirmPassword(), &f.confirm)
				case masterPwChange:
					field("mp:current", m.CurrentMasterPassword(), &f.current)
					field("mp:pw", m.NewMasterPassword(), &f.password)
					field("mp:confirm", m.ConfirmPassword(), &f.confirm)
				case masterPwSystem:
					field("mp:current", m.CurrentMasterPassword(), &f.current)
				}
			})
			if note != "" {
				ui.Text(c, note).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			}
			if f.err != "" {
				ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.masterPwOpen = false
				}
				if pill(c, action, true, false, f.busy) {
					a.SubmitMasterPassword(m)
				}
			})
		})
	}), tok)
	if !a.masterPwOpen && a.masterPw == f {
		// Cancelled: the action waiting on an unlock is dropped, as the
		// unlock failure's Cancel does.
		if f.unlocks() {
			a.unlock.after = nil
		}
		a.closeMasterPassword()
	}
}
