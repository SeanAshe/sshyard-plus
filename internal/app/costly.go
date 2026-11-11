package app

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strconv"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keys"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/vault"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// A backup or a PuTTY key whose password derivation costs more than usual
// (vault.ErrCostly, keys.ErrCostly) is opened only once the user agrees:
// the sheet here says what it takes, and Continue derives it. Nothing is
// derived before. Past the limits, the packages refuse it outright.

// costAsk is the sheet asking whether to derive a costly password.
type costAsk struct {
	// backup is a backup's password, else a PuTTY key's passphrase
	backup bool
	// memory is the most one derivation takes, in KiB; passes their passes
	memory, passes uint64
	// run is what Continue does
	run func()
	// busy is set while a key is derived, off the UI thread
	busy bool
}

// costlyKey is a PuTTY key derived once the user agreed, kept for the
// sheet that asked to save it: the converted key, or why it can not be.
type costlyKey struct {
	key keys.Key
	err error
}

// maxCostlyKeys bounds the costly keys kept for the session.
const maxCostlyKeys = 16

// errCostAsked stops a save that asked whether to derive a costly key;
// it runs again once the key is derived.
var errCostAsked = errors.New("app: asked whether to derive a costly key")

// askCost opens the sheet for q.
func (a *App) askCost(q *costAsk) {
	a.cost, a.costOpen = q, true
}

// closeCost closes the sheet; a key still being derived is dropped once
// derived, as the derivation can not be interrupted.
func (a *App) closeCost() {
	a.cost, a.costOpen = nil, false
}

// costlyID names a key file and the passphrase it is opened with,
// without keeping the passphrase: an HMAC under a key of the session.
func (a *App) costlyID(data, pass []byte) [32]byte {
	if a.costSalt == nil {
		a.costSalt = make([]byte, 32)
		if _, err := rand.Read(a.costSalt); err != nil {
			panic(err) // crypto/rand does not fail on supported systems
		}
	}
	h := hmac.New(sha256.New, a.costSalt)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(data)))
	h.Write(n[:])
	h.Write(data)
	h.Write(pass)
	var id [32]byte
	h.Sum(id[:0])
	return id
}

// loadKey is keys.Import, or the key derived for data and pass once the
// user agreed to its cost. Call it on the UI thread.
func (a *App) loadKey(data, pass []byte) (keys.Key, error) {
	if keys.IsPPK(data) && len(pass) > 0 && len(a.costKeys) > 0 {
		if k, ok := a.costKeys[a.costlyID(data, pass)]; ok {
			return k.key, k.err
		}
	}
	return importPlainKey(data, pass)
}

// What opens backups and keys, the costly ones once agreed to: variables
// so that tests can stand in for costly files without deriving them.
var (
	openVault       = vault.OpenWith
	importPlainKey  = keys.Import
	importCostlyKey = keys.ImportCostly
)

// askKeyCost asks, when err is a PuTTY key whose derivation costs more
// than usual, whether to derive it; it reports whether it asked, and the
// caller then stops. On Continue the key is derived off the UI thread,
// kept for loadKey, and retry runs on the UI thread. Cancelled while it
// is derived, the derivation runs to its end and its result is dropped.
func (a *App) askKeyCost(err error, data, pass []byte, retry func()) bool {
	var ce *keys.CostError
	if !errors.As(err, &ce) {
		return false
	}
	data, pass = bytes.Clone(data), bytes.Clone(pass)
	q := &costAsk{memory: ce.Memory, passes: ce.Passes}
	q.run = func() {
		q.busy = true
		go func() {
			k, err := importCostlyKey(data, pass)
			a.post(func() {
				defer clear(pass)
				if a.cost != q {
					return
				}
				if a.costKeys == nil || len(a.costKeys) >= maxCostlyKeys {
					a.costKeys = map[[32]byte]costlyKey{}
				}
				a.costKeys[a.costlyID(data, pass)] = costlyKey{k, err}
				a.closeCost()
				retry()
			})
		}()
	}
	a.askCost(q)
	return true
}

// costView is the sheet asking whether to derive a costly password.
func (a *App) costView(c *ui.Context, m locales.Messages) {
	q := a.cost
	if q == nil {
		return
	}
	tok := currentTokens(c)
	memory := humanSize(int64(q.memory) << 10)
	passes := strconv.FormatUint(q.passes, 10)
	sheet(stickyModal(c, &a.costOpen, func() {
		ui.Column(c).Gap(18).Width(440).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, icons.Warning, tok.Warning, 36)
				sheetTitle(c, m.CostlyTitle())
			})
			text := m.CostlyKey(memory, passes)
			if q.backup {
				text = m.CostlyBackup(memory, passes)
			}
			ui.Text(c, text).FontSize(theme.Body)
			if q.busy {
				ui.Text(c, m.CostlyBusyNote()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			}
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.costOpen = false
				}
				label := m.CostlyContinue()
				if q.busy {
					label = m.CostlyOpening()
				}
				b, clicked := pillEl(c, label, true, false, q.busy)
				if !q.busy {
					a.costHold.apply(c, q, b)
				}
				if clicked {
					q.run()
				}
			})
		})
	}), tok)
	if !a.costOpen && a.cost == q {
		a.cost = nil
	}
}
