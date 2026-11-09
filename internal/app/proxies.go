package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/internal/updater"
	"github.com/Licoy/sshyard/locales"
	sysproxy "github.com/egoist/mygo/netproxy"
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
	// back is the sheet whose proxy select opened this one ("New
	// proxy…"), shown again when it closes; nil from the settings
	back *proxyBack
}

// proxyBack is where a proxy select's "New proxy…" goes back to.
type proxyBack struct {
	// owner is the form of the sheet the select is on, kept while the
	// proxy sheet shows; nil for a select on a page
	owner any
	// done shows that sheet again, with id the proxy saved, "" none
	done func(id string)
}

func (f *proxyForm) proxyKind() assets.ProxyKind {
	if f.kind == 1 {
		return assets.ProxyHTTP
	}
	return assets.ProxySOCKS5
}

func (a *App) openProxyForm(p *assets.Proxy, back *proxyBack) {
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

// closeProxyForm closes the sheet, back to the sheet that opened it with
// saved, the ID of the proxy saved ("" none), chosen.
func (a *App) closeProxyForm(saved string) {
	f := a.proxyForm
	a.proxyForm, a.proxyEditing = nil, false
	if f != nil && f.back != nil {
		f.back.done(saved)
	}
}

// proxyKeeps reports whether the proxy sheet showing came from the
// proxy select of the sheet editing owner, which stays to go back to.
func (a *App) proxyKeeps(owner any) bool {
	return a.proxyForm != nil && a.proxyForm.back != nil && a.proxyForm.back.owner == owner
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
	a.proxiesChanged()
	a.closeProxyForm(saved.ID)
	return true
}

// proxyNew is the proxy select's "New proxy…" choice, never an ID.
const proxyNew = "+new"

// proxyPick is what a proxy select offers and where its "New proxy…"
// goes.
type proxyPick struct {
	// system offers the system's proxy settings as "", none then being
	// settings.ProxyDirect; without it none is ""
	system bool
	// owner is the form of the sheet the select is on, nil on a page.
	// leave hides that sheet for the proxy sheet; back shows it again,
	// with id the proxy saved ("" none)
	owner any
	leave func()
	back  func(id string)
}

// proxySelect is the one proxy select of everything that goes out
// through a proxy, bound to its ID: none (or the system's and none),
// every saved proxy by name, one deleted as such, and "New proxy…",
// which swaps the sheet for the proxy sheet and comes back with the new
// proxy chosen.
func (a *App) proxySelect(c *ui.Context, m locales.Messages, id *string, o proxyPick) ui.Element {
	names := map[string]string{proxyNew: m.NewProxyChoice()}
	var opts []string
	if o.system {
		opts = append(opts, "", settings.ProxyDirect)
		names[""], names[settings.ProxyDirect] = m.ProxySystem(), m.ProxyNone()
	} else {
		opts = append(opts, "")
		names[""] = m.ProxyNone()
	}
	for _, p := range a.store.Proxies() {
		opts = append(opts, p.ID)
		names[p.ID] = p.Name
	}
	if _, ok := names[*id]; !ok {
		opts = append(opts, *id)
		names[*id] = m.ProxyMissing()
	}
	opts = append(opts, proxyNew)
	sel := formSelectContent(c, id, opts, func(v string) {
		ui.Text(c, names[v]).FontSize(theme.Body).TextColor(currentTokens(c).Text).SingleLine().Grow(1).Shrink(1).MinWidth(0)
	}).Label(m.FieldProxy())
	// A choice lands between two builds: prev is the one before it.
	prev := ui.Local(sel, proxyPrevKey{}, func() string { return *id })
	if *id != proxyNew {
		*prev = *id
		return sel
	}
	*id = *prev
	if o.leave != nil {
		o.leave()
	}
	a.openProxyForm(nil, &proxyBack{owner: o.owner, done: o.back})
	return sel
}

type proxyPrevKey struct{}

// errProxyMissing is the dial of a route whose proxy was deleted: it
// goes nowhere rather than around it.
var errProxyMissing = errors.New("app: the chosen proxy was deleted")

// proxyRoute is how HTTP requests go out for the proxy choice id gives
// at each one (AIProvider.Proxy, Settings.UpdateProxy): "" through the
// system's proxy settings, PAC files included, settings.ProxyDirect
// straight, else through that saved proxy, looked up at each connection
// so that an edit applies to the next.
func (a *App) proxyRoute(id func() string) updater.Route {
	direct := a.p.Dial
	if direct == nil {
		direct = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	return updater.Route{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if id() == "" {
				return sysproxy.Proxy(req)
			}
			return nil, nil
		},
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			switch v := id(); v {
			case "", settings.ProxyDirect:
				return direct(ctx, network, addr)
			default:
				px, ok := a.store.Proxy(v)
				if !ok {
					return nil, errProxyMissing
				}
				return a.proxyDialer(px).DialContext(ctx, network, addr)
			}
		},
	}
}

// routeUpdates sends the update window's requests through the proxy the
// settings choose.
func (a *App) routeUpdates() {
	id := a.settings.UpdateProxy
	a.updProxy = id
	a.upd.SetRoute(a.proxyRoute(func() string { return id }))
}

// proxiesChanged closes the connections kept through a proxy that was
// edited or deleted: the next requests dial it anew.
func (a *App) proxiesChanged() {
	a.mu.Lock()
	for _, hc := range a.proxyClients {
		hc.CloseIdleConnections()
	}
	a.mu.Unlock()
	a.routeUpdates()
}

// proxyClient is the HTTP client of the proxy choice id, one per choice
// so that connections are kept between requests.
func (a *App) proxyClient(id string) *http.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	if hc, ok := a.proxyClients[id]; ok {
		return hc
	}
	r := a.proxyRoute(func() string { return id })
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy, t.DialContext = r.Proxy, r.Dial
	if a.proxyClients == nil {
		a.proxyClients = map[string]*http.Client{}
	}
	a.proxyClients[id] = &http.Client{Transport: t}
	return a.proxyClients[id]
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
		a.closeProxyForm("")
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
