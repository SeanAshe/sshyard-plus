package app

import (
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// proxyForm is the proxy editor's fields. Its password lives here only
// while the sheet is open; saving moves it into the keychain.
type proxyForm struct {
	isNew bool
	orig  assets.Proxy
	name  string
	// kind is the segmented control's index: 0 SOCKS5, 1 HTTP
	kind                int
	address, port, user string
	password, note      string
	err                 string
	// back is the host editor that opened the sheet ("New proxy…"),
	// shown again with the new proxy chosen
	back *hostForm
}

func (f *proxyForm) proxyKind() assets.ProxyKind {
	if f.kind == 1 {
		return assets.ProxyHTTP
	}
	return assets.ProxySOCKS5
}

func (a *App) openProxyForm(p *assets.Proxy, back *hostForm) {
	f := &proxyForm{isNew: p == nil, back: back, port: "1080"}
	if p != nil {
		f.orig = *p
		f.name, f.address, f.port, f.user, f.note = p.Name, p.Address, strconv.Itoa(p.Port), p.User, p.Note
		if p.Kind == assets.ProxyHTTP {
			f.kind = 1
		}
	}
	a.proxyForm, a.proxyEditing = f, true
}

// closeProxyForm closes the sheet, back to the host editor that opened it.
func (a *App) closeProxyForm() {
	f := a.proxyForm
	a.proxyForm, a.proxyEditing = nil, false
	if f != nil && f.back != nil {
		a.form, a.editing = f.back, true
	}
}

// SaveProxyForm validates the sheet, saves the proxy and puts its
// password in the keychain.
func (a *App) SaveProxyForm(m locales.Messages) bool {
	f := a.proxyForm
	if f == nil {
		return false
	}
	f.err = ""
	port, err := strconv.Atoi(strings.TrimSpace(f.port))
	if err != nil || port < 1 || port > 65535 {
		f.err = m.InvalidPort()
		return false
	}
	p := f.orig
	p.Name, p.Kind, p.Address, p.Port, p.User, p.Note = f.name, f.proxyKind(), f.address, port, f.user, f.note
	// The password first, then the entry; a failure puts the old
	// password back.
	if f.isNew {
		p.ID = assets.NewID()
	}
	tx := &secretTx{kc: a.kc}
	fail := func(msg string) bool {
		if rerr := tx.rollback(); rerr != nil {
			msg = m.ErrKeychainRestore(msg, secretStoreErrorText(m, rerr))
		}
		f.err = msg
		return false
	}
	if !a.unlocked() && (f.password != "" || (!f.isNew && f.orig.User != "" && strings.TrimSpace(p.User) == "")) {
		draft := a.proxyForm
		a.withSecrets(func() {
			if a.proxyForm == draft {
				a.SaveProxyForm(m)
			}
		})
		return false
	}
	key := keychain.ProxyPassword(p.ID)
	switch {
	case !f.isNew && f.orig.User != "" && strings.TrimSpace(p.User) == "":
		err = tx.delete(key)
	case f.password != "":
		err = tx.set(key, []byte(f.password))
	}
	if err != nil {
		return fail(m.ErrKeychainSave(secretStoreErrorText(m, err)))
	}
	var saved assets.Proxy
	if f.isNew {
		saved, err = a.store.AddProxy(p)
	} else {
		saved, err = a.store.SaveProxy(p)
	}
	if err != nil {
		return fail(fieldError(m, err))
	}
	if f.back != nil {
		f.back.proxy = saved.Name
	}
	a.closeProxyForm()
	return true
}

// proxyChoices are the host editor's proxy select: none, then every
// proxy by name.
func (a *App) proxyChoices(m locales.Messages) (labels []string, ids []string) {
	labels, ids = []string{m.ProxyNone()}, []string{""}
	for _, p := range a.store.Proxies() {
		labels, ids = append(labels, p.Name), append(ids, p.ID)
	}
	return
}

func proxyKindText(p assets.Proxy) string {
	if p.Kind == assets.ProxyHTTP {
		return "HTTP"
	}
	return "SOCKS5"
}

func (a *App) proxyView(c *ui.Context, m locales.Messages) {
	f := a.proxyForm
	if f == nil {
		return
	}
	tok := currentTokens(c)
	sheet(ui.Modal(c, &a.proxyEditing, func() {
		ui.Column(c).Gap(18).Width(480).Children(func() {
			title := m.EditProxy()
			if f.isNew {
				title = m.NewProxy()
			}
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tint := theme.Brand
				if !f.isNew {
					tint = tok.Tint(f.orig.ID)
				}
				tile(c, icons.Proxy, tint, 36)
				ui.Column(c).Gap(1).Shrink(1).Children(func() {
					sheetTitle(c, title)
					if f.name != "" {
						ui.Text(c, f.name).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine()
					}
				})
			})
			ui.Form(c, func() {
				ui.Field(c, m.FieldName(), func() {
					in := formTextInput(c.Key("pf:name"), &f.name).Placeholder("office").AutoFocus()
					a.proxyHold.apply(c, f, in)
				})
				ui.Field(c, m.FieldType(), func() {
					prev := f.kind
					if formSegmented(c.Key("pf:kind"), &f.kind, false, "SOCKS5", "HTTP").Label(m.FieldType()).Changed() {
						// The usual port follows the kind, unless one was typed.
						def := map[int]string{0: "1080", 1: "8080"}
						if strings.TrimSpace(f.port) == "" || f.port == def[prev] {
							f.port = def[f.kind]
						}
					}
				})
				ui.Field(c, m.FieldHost(), func() { formTextInput(c.Key("pf:addr"), &f.address).Placeholder("proxy.example.com") })
				ui.Field(c, m.FieldPort(), func() { formTextInput(c.Key("pf:port"), &f.port).Width(90) })
				ui.Field(c, m.FieldUser(), func() { formTextInput(c.Key("pf:user"), &f.user).Placeholder(m.ProxyUserPlaceholder()) })
				if strings.TrimSpace(f.user) != "" {
					ui.Field(c, m.FieldPassword(), func() {
						ph := ""
						if !f.isNew && f.orig.User != "" {
							ph = m.PasswordUnchanged()
						}
						formTextInput(c.Key("pf:pw"), &f.password).Password().Placeholder(ph)
					})
				}
				ui.Field(c, m.FieldNote(), func() { formTextInput(c.Key("pf:note"), &f.note) })
			})
			ui.Text(c, m.ProxyFirstHop()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			if !f.isNew {
				used := m.CredUnused()
				if hs := a.store.ProxyUsers(f.orig.ID); len(hs) > 0 {
					used = m.CredUsedBy(hostNames(hs))
				}
				ui.Text(c, used).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			}
			if f.err != "" {
				ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				if !f.isNew && pill(c, m.Delete(), false, true, false) {
					a.runSecretAction(func() error {
						if a.proxyForm != f {
							return nil
						}
						if err := a.DeleteProxy(f.orig.ID); err != nil {
							return err
						}
						a.proxyEditing = false
						return nil
					}, func(err error) { f.err = a.inUseText(m, err) })
				}
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.proxyEditing = false
				}
				if pill(c, m.Save(), true, false, false) {
					a.SaveProxyForm(m)
				}
			})
		})
	}), tok)
	if !a.proxyEditing && a.proxyForm == f {
		a.closeProxyForm()
	}
}

// proxiesSection is the settings' list of proxies.
func (a *App) proxiesSection(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	a.settingsSection(c, tok, nil, ui.Color{}, "", func(g *settingsGroup) {
		ps := a.store.Proxies()
		if len(ps) == 0 {
			g.row(m.NoProxies(), "", func() {})
		}
		for _, p := range ps {
			p := p
			note := a.pv(proxyKindText(p) + " · " + p.Addr())
			if p.User != "" {
				note += " · " + p.User
			}
			if hs := a.store.ProxyUsers(p.ID); len(hs) > 0 {
				note += " — " + m.CredUsedBy(hostNames(hs))
			}
			g.row(p.Name, note, func() {
				if iconButton(c, "proxy:edit:"+p.ID, icons.Edit, m.Edit()) {
					a.openProxyForm(&p, nil)
				}
				if iconButton(c, "proxy:del:"+p.ID, icons.Delete, m.Delete()) {
					a.runSecretAction(func() error { return a.DeleteProxy(p.ID) }, func(err error) { a.toastError(a.inUseText(m, err)) })
				}
			})
		}
		g.row("", "", func() {
			if pill(c, m.NewProxy(), false, false, false) {
				a.openProxyForm(nil, nil)
			}
		})
	})
}
