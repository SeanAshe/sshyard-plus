package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/keys"
	"github.com/Licoy/sshyard/internal/sshconfig"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// Import from ~/.ssh/config: the hosts it names, previewed to choose,
// rename and group, each conflict with a saved host (same address, port
// and user) skipped, overwritten or kept beside; IdentityFiles become key
// credentials, one per file, their secrets in the keychain; ProxyJump
// becomes a jump chain over imported or saved hosts, or hosts made for
// it. The config and the key files are only read.

// impKey is a key file a host of the config names.
type impKey struct {
	path string
	data []byte
	key  keys.Key
	// err is why the file cannot be imported (unreadable, not a key); nil
	// when it can, if need be with a passphrase
	err error
	// encrypted keys take a passphrase typed now, kept in the keychain;
	// with none, an OpenSSH key is asked for at each connection
	encrypted bool
	pass      string
	// cred is the credential made for it (or a saved one of the same key)
	cred string
}

// readable reports whether the key can be imported as it stands.
func (k *impKey) usable() bool { return k != nil && k.err == nil }

// needsPass reports a key that cannot be kept without its passphrase:
// a PuTTY key (converted with it), a legacy PEM key with no public half.
func (k *impKey) needsPass() bool {
	return k.encrypted && (keys.IsPPK(k.data) || k.key.Public == nil)
}

// impRow is one host of the preview.
type impRow struct {
	e           sshconfig.Entry
	pick        bool
	name, group string
	// conflict is the saved host at the same address, port and user;
	// action what to do then
	conflict string
	action   string
	key      *impKey
	// target is user@address:port as it will be saved
	user string
	port int
}

func (r *impRow) target() string {
	return r.user + "@" + r.e.HostName + ":" + strconv.Itoa(r.port)
}

type importForm struct {
	path    string
	rows    []*impRow
	skipped []sshconfig.Entry
	keys    map[string]*impKey
	order   []*impKey // keys in the order the hosts name them
	cfg     *sshconfig.Config
	err     string
	notes   []string // lines the config could not be read at
}

// importActions are the choices for a conflict, the first the default.
func importActions(m locales.Messages) []string {
	return []string{m.ImportSkip(), m.ImportOverwrite(), m.ImportKeepBoth()}
}

// sshConfigPath is the config the import reads by default.
func (a *App) sshConfigPath() string {
	home := a.p.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".ssh", "config")
}

func (a *App) homeDir() string {
	if a.p.HomeDir != "" {
		return a.p.HomeDir
	}
	h, _ := os.UserHomeDir()
	return h
}

// OpenImport reads the config at path ("" ~/.ssh/config) and shows the
// preview.
func (a *App) OpenImport(m locales.Messages, path string) {
	if path == "" {
		path = a.sshConfigPath()
	}
	f := &importForm{path: path, keys: map[string]*impKey{}}
	a.imp, a.importing = f, true
	cfg, err := sshconfig.Parse(path, sshconfig.Env{Home: a.homeDir(), LocalUser: a.localUser()})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			f.err = m.ImportNoConfig(path)
		} else {
			f.err = m.ErrReadFile(filepath.Base(path), err.Error())
		}
		return
	}
	f.cfg, f.notes = cfg, cfg.Errors
	actions := importActions(m)
	for _, e := range cfg.Entries {
		if e.Pattern {
			f.skipped = append(f.skipped, e)
			continue
		}
		r := &impRow{e: e, pick: true, name: e.Alias, user: e.User, port: e.Port}
		if r.user == "" {
			r.user = a.localUser()
		}
		if r.port == 0 {
			r.port = 22
		}
		if h, ok := a.findHost(e.HostName, r.port, r.user); ok {
			r.conflict, r.action, r.pick = h.ID, actions[0], true
		}
		r.key = a.importKey(f, e.IdentityFiles)
		f.rows = append(f.rows, r)
	}
	// The keys of the hosts made for jumps too, so their passphrases are
	// asked here.
	for _, r := range f.rows {
		for _, hop := range r.e.ProxyJump {
			if f.row(hop.Host) == nil {
				a.importKey(f, cfg.Resolve(hop.Host).IdentityFiles)
			}
		}
	}
}

// row is the preview row of alias, nil for none.
func (f *importForm) row(alias string) *impRow {
	for _, r := range f.rows {
		if strings.EqualFold(r.e.Alias, alias) {
			return r
		}
	}
	return nil
}

// importKey reads the first of files that exists, once per path; nil
// when there is none. A file that exists but cannot be read is kept with
// its error, as is the first when none exists.
func (a *App) importKey(f *importForm, files []string) *impKey {
	if len(files) == 0 {
		return nil
	}
	path := files[0]
	for _, p := range files {
		if _, err := os.Stat(p); err == nil {
			path = p
			break
		}
	}
	if k, ok := f.keys[path]; ok {
		return k
	}
	k := &impKey{path: path}
	f.keys[path] = k
	f.order = append(f.order, k)
	k.data, k.err = readKeyFile(path)
	if k.err != nil {
		return k
	}
	k.key, k.err = keys.Import(k.data, nil)
	if errors.Is(k.err, keys.ErrPassphraseNeeded) {
		k.encrypted, k.err = true, nil
	}
	if k.err == nil && k.key.Fingerprint() != "" {
		for _, cr := range a.store.Credentials() {
			if cr.Kind == assets.CredKey && cr.Fingerprint == k.key.Fingerprint() {
				k.cred = cr.ID
			}
		}
	}
	return k
}

// findHost is the saved host at address, port and user.
func (a *App) findHost(addr string, port int, user string) (assets.Host, bool) {
	for _, h := range a.store.List() {
		if strings.EqualFold(h.Address, addr) && h.Port == port && h.User == user {
			return h, true
		}
	}
	return assets.Host{}, false
}

// importResult counts what an import did.
type importResult struct {
	added, overwritten, skipped, jumps, creds int
	errs                                      []string
}

// Import saves the chosen hosts of the preview.
func (a *App) Import(m locales.Messages) (importResult, bool) {
	f := a.imp
	var res importResult
	if f == nil || f.cfg == nil {
		return res, false
	}
	f.err = ""
	actions := importActions(m)
	// Every key a chosen host uses must be importable before anything is
	// saved: a passphrase typed must open it.
	for _, k := range f.order {
		if !k.usable() || !a.keyWanted(m, f, k) {
			continue
		}
		if k.pass != "" {
			if _, err := a.loadKey(k.data, []byte(k.pass)); err != nil {
				if a.askKeyCost(err, k.data, []byte(k.pass), func() { a.retryImport(m, f) }) {
					return res, false
				}
				f.err = m.ImportKeyError(filepath.Base(k.path), keyError(m, err))
				return res, false
			}
		} else if k.needsPass() && k.cred == "" {
			f.err = m.ImportKeyNeedsPass(filepath.Base(k.path))
			return res, false
		}
	}
	if !a.unlocked() && a.importNeedsSecrets(m, f) {
		a.withSecrets(func() { a.retryImport(m, f) })
		return res, false
	}
	ids := map[string]string{}   // alias → host ID
	res.skipped = len(f.skipped) // as the preview counts them
	for _, r := range f.rows {
		if !r.pick {
			continue
		}
		if r.conflict != "" && r.action == actions[0] {
			ids[strings.ToLower(r.e.Alias)] = r.conflict
			res.skipped++
			continue
		}
		h := assets.Host{CwdFollow: true}
		if r.conflict != "" && r.action == actions[1] {
			if old, ok := a.store.Get(r.conflict); ok {
				h = old
			}
		}
		h.Name, h.Address, h.Port, h.User, h.Group = r.name, r.e.HostName, r.port, r.user, r.group
		if strings.TrimSpace(h.Name) == "" {
			h.Name = r.e.Alias
		}
		if err := a.importAuth(m, f, &h, r.key, &res); err != nil {
			res.errs = append(res.errs, r.e.Alias+": "+err.Error())
		}
		h.JumpChain = nil
		var err error
		if h.ID != "" {
			var changes []keychain.Change
			if old, ok := a.store.Get(h.ID); ok && (old.Auth == assets.AuthPassword || old.Auth == assets.AuthKey) {
				changes = hostSecretDeletes(h.ID)
			}
			h, err = a.saveHostSecrets(h, false, changes)
			res.overwritten++
		} else {
			h, err = a.store.Add(h)
			res.added++
		}
		if err != nil {
			res.errs = append(res.errs, r.e.Alias+": "+fieldError(m, err))
			continue
		}
		ids[strings.ToLower(r.e.Alias)] = h.ID
	}
	// Then the jump chains, which may name hosts saved above.
	for _, r := range f.rows {
		id, ok := ids[strings.ToLower(r.e.Alias)]
		if !r.pick || !ok || id == r.conflict && r.action == actions[0] || len(r.e.ProxyJump) == 0 {
			continue
		}
		a.importChain(m, f, id, r.e, ids, &res, 0)
	}
	return res, true
}

// retryImport runs the import of f again, once what stopped it (the
// secrets locked, a costly key) is out of the way, and says how it went.
func (a *App) retryImport(m locales.Messages, f *importForm) {
	if a.imp != f {
		return
	}
	if result, ok := a.Import(m); ok {
		a.importing = false
		a.toast(func(m locales.Messages) ui.Toast {
			return ui.Toast{Title: m.ImportDone(strconv.Itoa(result.added+result.overwritten), strconv.Itoa(result.skipped)), Type: "success"}
		})
		if len(result.errs) > 0 {
			a.toastError(strings.Join(result.errs, "\n"))
		}
	}
}

func (a *App) importNeedsSecrets(m locales.Messages, f *importForm) bool {
	for _, k := range f.order {
		if k.usable() && a.keyWanted(m, f, k) && k.cred == "" {
			return true
		}
	}
	for _, r := range f.rows {
		if r.pick && r.conflict != "" && r.action == importActions(m)[1] {
			if h, ok := a.store.Get(r.conflict); ok && (h.Auth == assets.AuthPassword || h.Auth == assets.AuthKey) {
				return true
			}
		}
	}
	return false
}

// keyWanted reports whether a chosen host (or a host made for a jump)
// uses k.
func (a *App) keyWanted(m locales.Messages, f *importForm, k *impKey) bool {
	actions := importActions(m)
	for _, r := range f.rows {
		if r.key == k && r.pick && !(r.conflict != "" && r.action == actions[0]) {
			return true
		}
	}
	for _, r := range f.rows {
		for _, hop := range r.e.ProxyJump {
			if r.pick && f.row(hop.Host) == nil && f.keys[firstOr(f.cfg.Resolve(hop.Host).IdentityFiles)] == k {
				return true
			}
		}
	}
	return false
}

func firstOr(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// importAuth points h at the credential of k, made now if need be; with
// no usable key, h asks for its password.
func (a *App) importAuth(m locales.Messages, f *importForm, h *assets.Host, k *impKey, res *importResult) error {
	h.Auth, h.CredentialID = assets.AuthAsk, ""
	if k == nil {
		return nil
	}
	if !k.usable() {
		return errors.New(m.ImportKeyUnreadable(k.path))
	}
	if k.cred == "" {
		id, err := a.importCredential(m, k)
		if err != nil {
			return err
		}
		k.cred = id
		res.creds++
	}
	h.Auth, h.CredentialID = assets.AuthCredential, k.cred
	return nil
}

// importCredential saves k as a key credential: the key, and its
// passphrase when one was typed, in the keychain first.
func (a *App) importCredential(m locales.Messages, k *impKey) (string, error) {
	key := k.key
	if k.pass != "" {
		var err error
		if key, err = a.loadKey(k.data, []byte(k.pass)); err != nil {
			return "", errors.New(keyError(m, err))
		}
	}
	cr := assets.Credential{
		ID: assets.NewID(), Name: a.freeCredName(filepath.Base(k.path)), Kind: assets.CredKey,
		KeyType: key.Type(), Fingerprint: key.Fingerprint(), Encrypted: key.Encrypted,
		AskPassphrase: key.Encrypted && k.pass == "",
		Note:          m.ImportedFrom(k.path),
	}
	// As OpenSSH, the certificate beside the key goes with it.
	if c, err := keys.ParseCert(certBeside(k.path)); err == nil && c.Matches(key.Fingerprint()) {
		cr.Certificate = c.Line
	}
	tx := &secretTx{kc: a.kc}
	fail := func(msg string) (string, error) {
		if rerr := tx.rollback(); rerr != nil {
			msg = m.ErrKeychainRestore(msg, secretStoreErrorText(m, rerr))
		}
		return "", errors.New(msg)
	}
	if err := tx.set(keychain.Cred(cr.ID, keychain.PrivateKey), key.PEM); err != nil {
		return fail(m.ErrKeychainSave(secretStoreErrorText(m, err)))
	}
	if key.Encrypted && k.pass != "" {
		if err := tx.set(keychain.Cred(cr.ID, keychain.Passphrase), []byte(k.pass)); err != nil {
			return fail(m.ErrKeychainSave(secretStoreErrorText(m, err)))
		}
	}
	if _, err := a.store.AddCredential(cr); err != nil {
		return fail(m.ErrGeneric(err.Error()))
	}
	return cr.ID, nil
}

// freeCredName is name, or name (2) and on, whichever no credential has.
func (a *App) freeCredName(name string) string {
	taken := map[string]bool{}
	for _, cr := range a.store.Credentials() {
		taken[strings.ToLower(cr.Name)] = true
	}
	out := name
	for i := 2; taken[strings.ToLower(out)]; i++ {
		out = fmt.Sprintf("%s (%d)", name, i)
	}
	return out
}

// importChain sets the jump chain of the host id from e's ProxyJump:
// each hop a host of the import, else a saved host at its address, else
// a host made for it (with its own ProxyJump in turn).
func (a *App) importChain(m locales.Messages, f *importForm, id string, e sshconfig.Entry, ids map[string]string, res *importResult, depth int) {
	if depth > 8 {
		return
	}
	var chain []string
	for _, hop := range e.ProxyJump {
		jid, err := a.importHop(m, f, hop, ids, res, depth)
		if err != nil {
			res.errs = append(res.errs, e.Alias+": "+err.Error())
			return
		}
		chain = append(chain, jid)
	}
	h, ok := a.store.Get(id)
	if !ok {
		return
	}
	h.JumpChain = chain
	if _, err := a.store.Update(h); err != nil {
		msg := fieldError(m, err)
		if errors.Is(err, assets.ErrJumpLoop) {
			msg = m.ErrJumpLoop()
		}
		res.errs = append(res.errs, e.Alias+": "+msg)
	}
}

func (a *App) importHop(m locales.Messages, f *importForm, hop sshconfig.Hop, ids map[string]string, res *importResult, depth int) (string, error) {
	alias := strings.ToLower(hop.Host)
	if id, ok := ids[alias]; ok && hop.User == "" && hop.Port == 0 {
		return id, nil
	}
	r := f.cfg.Resolve(hop.Host)
	user, port := hop.User, hop.Port
	if user == "" {
		user = r.User
	}
	if user == "" {
		user = a.localUser()
	}
	if port == 0 {
		port = r.Port
	}
	if port == 0 {
		port = 22
	}
	if h, ok := a.findHost(r.HostName, port, user); ok {
		return h.ID, nil
	}
	h := assets.Host{Name: hop.Host, Address: r.HostName, Port: port, User: user, CwdFollow: true}
	if row := f.row(hop.Host); row != nil {
		h.Name, h.Group = row.name, row.group
	}
	if err := a.importAuth(m, f, &h, a.importKey(f, r.IdentityFiles), res); err != nil {
		res.errs = append(res.errs, hop.Host+": "+err.Error())
	}
	h, err := a.store.Add(h)
	if err != nil {
		return "", errors.New(fieldError(m, err))
	}
	res.jumps++
	if hop.User == "" && hop.Port == 0 {
		ids[alias] = h.ID
	}
	if len(r.ProxyJump) > 0 {
		a.importChain(m, f, h.ID, r, ids, res, depth+1)
	}
	return h.ID, nil
}

// importCounts are the preview's numbers: hosts to save, skipped (the
// patterns, and conflicts to skip), and chosen in all.
func (a *App) importCounts(m locales.Messages, f *importForm) (n, skipped, picked int) {
	actions := importActions(m)
	skipped = len(f.skipped)
	for _, r := range f.rows {
		switch {
		case !r.pick:
		case r.conflict != "" && r.action == actions[0]:
			skipped++
			picked++
		default:
			n++
			picked++
		}
	}
	return n, skipped, picked
}

func (a *App) pickImportFile(m locales.Messages) {
	if a.p.PickFile == nil {
		return
	}
	title := m.ImportChooseFile()
	go func() {
		path, err := a.p.PickFile(title)
		if path == "" || err != nil {
			return
		}
		a.post(func() { a.OpenImport(m, path) })
	}()
}

func (a *App) importView(c *ui.Context, m locales.Messages) {
	f := a.imp
	if f == nil {
		return
	}
	tok := currentTokens(c)
	sheet(ui.Modal(c, &a.importing, func() {
		ui.Column(c).Gap(14).Width(760).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, icons.Import, theme.Brand, 36)
				ui.Column(c).Gap(1).Shrink(1).Grow(1).Children(func() {
					sheetTitle(c, m.ImportSSHConfig())
					ui.Text(c, f.path).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine()
				})
				if a.p.PickFile != nil {
					if (IconButton{Icon: icons.OpenFile, Label: m.ImportChooseFile()}).Clicked(c, "imp:pick") {
						a.pickImportFile(m)
					}
				}
			})
			if f.cfg != nil {
				n, skipped, _ := a.importCounts(m, f)
				ui.Text(c, m.ImportSummary(strconv.Itoa(n), strconv.Itoa(skipped))).FontSize(theme.Body).FontWeight(500)
				sheetScroll(ui.Scroll(c.Key("imp:rows")).MaxHeight(420)).Children(func() {
					ui.Column(c).Gap(8).FillWidth().Children(func() {
						for i, r := range f.rows {
							a.importRowView(c, m, tok, f, i, r)
						}
						for _, e := range f.skipped {
							ui.Row(c.Key("imp:skip:"+e.Alias)).Gap(8).Padding(8, 10).Radius(theme.RadiusControl).Background(tok.Fill).AlignItems(ui.Center).Children(func() {
								ui.Text(c, e.Alias).FontSize(theme.Body).TextColor(tok.TextMuted).SingleLine()
								ui.Text(c, m.ImportSkippedPattern()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
							})
						}
						a.importKeysView(c, m, tok, f)
					})
				})
				for _, n := range f.notes {
					ui.Text(c, n).FontSize(theme.Caption).TextColor(tok.TextMuted)
				}
				ui.Text(c, m.ImportReadOnlyNote()).FontSize(theme.Footnote).TextColor(tok.TextMuted)
			}
			if f.err != "" {
				ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.importing = false
				}
				n := 0
				if f.cfg != nil {
					n, _, _ = a.importCounts(m, f)
				}
				if pill(c, m.ImportButton(strconv.Itoa(n)), true, false, n == 0) {
					if res, ok := a.Import(m); ok {
						a.importing = false
						c.AddToast(ui.Toast{Title: m.ImportDone(strconv.Itoa(res.added+res.overwritten), strconv.Itoa(res.skipped)), Type: "success"})
						if len(res.errs) > 0 {
							c.AddToast(ui.Toast{Title: strings.Join(res.errs, "\n"), Type: "error"})
						}
					}
				}
			})
		})
	}), tok)
	if !a.importing {
		for _, k := range f.order {
			k.pass = ""
		}
		a.imp = nil
	}
}

func (a *App) importRowView(c *ui.Context, m locales.Messages, tok theme.Tokens, f *importForm, i int, r *impRow) {
	key := "imp:" + strconv.Itoa(i)
	ui.Column(c.Key(key)).Gap(6).Padding(8, 10).Radius(theme.RadiusControl).Border(1, tok.Line).FillWidth().Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			formCheckbox(c.Key(key+":pick"), &r.pick, "").Label(m.ImportPick(r.e.Alias))
			formTextInput(c.Key(key+":name"), &r.name).Label(m.FieldName()).Width(170)
			ui.Text(c, r.target()).FontSize(theme.Footnote).TextColor(tok.TextMuted).SingleLine().Grow(1).Shrink(1)
			groups := a.groups()
			formCombobox(c.Key(key+":group"), &r.group, groups).Label(m.FieldGroup()).Width(150)
			if r.conflict != "" {
				formSelect(c.Key(key+":act"), &r.action, importActions(m)).Label(m.ImportConflictAction()).Width(130)
			}
		})
		var notes []string
		if r.conflict != "" {
			notes = append(notes, m.ImportConflict(a.hostLabel(r.conflict)))
		}
		if len(r.e.ProxyJump) > 0 {
			var hops []string
			for _, h := range r.e.ProxyJump {
				hops = append(hops, h.String())
			}
			notes = append(notes, m.ImportVia(strings.Join(hops, " → ")))
		}
		if r.key != nil {
			if r.key.usable() {
				notes = append(notes, m.ImportKeyFile(filepath.Base(r.key.path)))
			}
		}
		if len(notes) > 0 {
			ui.Text(c, strings.Join(notes, " · ")).FontSize(theme.Footnote).TextColor(tok.TextMuted).Margin(0, 0, 0, 26)
		}
		if r.key != nil && !r.key.usable() {
			warnLine(c, tok, m.ImportKeyUnreadable(r.key.path))
		}
		if len(r.e.Unsupported) > 0 {
			warnLine(c, tok, m.ImportNotImported(strings.Join(r.e.Unsupported, ", ")))
		}
	})
}

// warnLine is a footnote with the warning icon.
func warnLine(c *ui.Context, tok theme.Tokens, s string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Margin(0, 0, 0, 26).Children(func() {
		ui.Icon(c, icons.Warning.SVG).Size(12, 12).Shrink(0).TextColor(tok.Warning)
		ui.Text(c, s).FontSize(theme.Footnote).TextColor(tok.Warning).Shrink(1)
	})
}

// importKeysView asks the passphrases of the encrypted keys.
func (a *App) importKeysView(c *ui.Context, m locales.Messages, tok theme.Tokens, f *importForm) {
	var enc []*impKey
	for _, k := range f.order {
		if k.usable() && k.encrypted && k.cred == "" {
			enc = append(enc, k)
		}
	}
	if len(enc) == 0 {
		return
	}
	ui.Text(c, m.ImportPassphrases()).FontSize(theme.Footnote).FontWeight(600).Margin(6, 0, 0, 0)
	for i, k := range enc {
		ui.Row(c.Key("imp:key:" + strconv.Itoa(i))).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Icon(c, icons.Key.SVG).Size(14, 14).Shrink(0).TextColor(tok.TextMuted)
			ui.Text(c, k.path).FontSize(theme.Footnote).SingleLine().Shrink(1).Grow(1)
			ph := m.ImportPassphraseAsk()
			if k.needsPass() {
				ph = m.ImportPassphraseNeeded()
			}
			formTextInput(c.Key("imp:pass:"+k.path), &k.pass).Password().Placeholder(ph).Label(m.ImportPassphraseFor(filepath.Base(k.path))).Width(260)
		})
	}
}

// groups are the groups the saved hosts are in.
func (a *App) groups() []string {
	_, groups, _ := hostsBy(a.store, "")
	return groups
}
