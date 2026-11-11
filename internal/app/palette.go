package app

import (
	"runtime"
	"slices"
	"strings"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keymap"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The command palette finds what the app can do by what it is called,
// and does it: the actions with a shortcut, the app's commands, every
// settings page and setting, the themes and color schemes, the hosts to
// connect to and the snippets to send.

// paletteState is the command palette's: whether it shows, what is typed
// and the row chosen.
type paletteState struct {
	open  bool
	query string
	sel   int
	// reveal scrolls to the row chosen, after the keyboard moved it
	reveal bool
	// gen counts the openings, for the hold of the focus on the field
	gen  int
	hold focusHold
}

// paletteMax is how many rows the palette shows at most.
const paletteMax = 60

// paletteItem is one thing the palette finds: its title and the line
// under it, what else finds it, the shortcut beside it, and what it does.
type paletteItem struct {
	key   string
	icon  *icons.Icon
	title string
	sub   string
	// also is matched as the title is, after it: the title in English, a
	// page's description
	also  string
	combo keymap.Combo
	// on marks the choice in force, as the color scheme used
	on  bool
	run func()
}

// openPalette shows the command palette, empty.
func (a *App) openPalette() {
	a.palette = paletteState{open: true, gen: a.palette.gen + 1}
}

// paletteItems are everything the palette finds, in the order it lists
// them when they match as well.
func (a *App) paletteItems(m locales.Messages, tok theme.Tokens) []paletteItem {
	en := locales.In("en")
	var out []paletteItem
	add := func(it paletteItem) { out = append(out, it) }
	cmd := func(key string, ic *icons.Icon, label func(locales.Messages) string, run func()) {
		add(paletteItem{key: key, icon: ic, title: label(m), sub: m.PaletteCommand(), also: label(en), run: run})
	}
	for _, ac := range actions {
		if ac.editor || ac.id == actCommandPalette {
			continue
		}
		id := ac.id
		add(paletteItem{key: "act:" + id, icon: ac.icon, title: ac.label(m), sub: m.PaletteCommand(), also: ac.label(en),
			combo: a.keyOf(id), run: func() { a.runAction(id) }})
	}
	cmd("newhost", icons.Host, locales.Messages.NewHost, func() { a.openEditor(nil) })
	cmd("newgroup", icons.NewFolder, locales.Messages.NewGroup, func() { a.openGroupForm("", "") })
	cmd("localterm", icons.LocalShell, locales.Messages.LocalTerminal, func() { a.openLocal(m, "", false) })
	if len(a.shells) > 1 {
		for _, sh := range a.shells {
			add(paletteItem{key: "localterm:" + sh.ID, icon: icons.LocalShell, title: m.LocalTerminalWith(sh.Name), sub: m.PaletteCommand(),
				also: en.LocalTerminalWith(sh.Name), run: func() { a.openLocal(m, sh.ID, false) }})
		}
	}
	if a.store.Len() > 0 {
		cmd("newfwd", icons.Forward, locales.Messages.NewForwardMenu, func() { a.openFwdForm(m, nil, a.selectedHost()) })
	}
	cmd("import", icons.Import, locales.Messages.ImportSSHConfigMenu, func() { a.OpenImport(m, "") })
	add(paletteItem{key: "privacy", icon: icons.Privacy, title: m.PrivacyMode(), sub: m.PaletteCommand(), also: en.PrivacyMode(),
		on: a.settings.Privacy, run: func() {
			if err := a.TogglePrivacy(); err != nil {
				a.toastError(m.ErrGeneric(err.Error()))
			}
		}})
	cmd("update", icons.Update, locales.Messages.CheckForUpdates, func() { a.upd.Check() })
	cmd("settings", icons.Settings, locales.Messages.Settings, func() { a.openSettings(a.prefNow()) })

	for _, h := range a.store.List() {
		id := h.ID
		sub := h.Address
		if !h.SSH() {
			sub = h.Target()
		}
		add(paletteItem{key: "host:" + id, icon: icons.Host, title: h.Label(), sub: a.pv(sub), run: func() { a.Connect(id, openAt{}) }})
	}
	for _, n := range a.store.Snippets() {
		add(paletteItem{key: "snippet:" + n.ID, icon: icons.Send, title: n.Name, sub: m.Snippets(), also: n.Group,
			run: func() { a.paletteSnippet(m, n.ID, n.SendEnter) }})
	}

	for _, g := range prefGroups(m) {
		for _, it := range g.items {
			page := it.page
			add(paletteItem{key: "page:" + string(page), icon: it.icon, title: it.title, sub: m.Settings(), also: it.desc,
				run: func() { a.openSettings(page) }})
		}
	}
	titles := map[prefPage]string{}
	pageIcons := map[prefPage]*icons.Icon{}
	for _, g := range prefGroups(m) {
		for _, it := range g.items {
			titles[it.page], pageIcons[it.page] = it.title, it.icon
		}
	}
	for _, st := range settingsIndex() {
		// the actions are there already, as commands
		if st.page == prefShortcuts {
			continue
		}
		page, label := st.page, st.label(m)
		add(paletteItem{key: "pref:" + string(page) + ":" + st.label(en), icon: pageIcons[page], title: label,
			sub: m.PaletteInSettings(titles[page]), also: st.label(en), run: func() { a.openSetting(page, label) }})
	}

	for i, md := range theme.Modes {
		name := []func(locales.Messages) string{locales.Messages.ThemeAuto, locales.Messages.ThemeLight, locales.Messages.ThemeDark}[i]
		add(paletteItem{key: "theme:" + string(md), icon: themeIcon(md), title: name(m), sub: m.Theme(), also: name(en),
			on: a.settings.Theme == md, run: func() { a.chooseSetting(m, func(s *settings.Settings) { s.Theme = md }) }})
	}
	scheme := func(id, name string) {
		add(paletteItem{key: "scheme:" + id, icon: icons.Appearance, title: name, sub: m.ColorScheme(),
			on: a.settings.TerminalScheme == id, run: func() { a.chooseSetting(m, func(s *settings.Settings) { s.TerminalScheme = id }) }})
	}
	scheme("", m.SchemeFollowAppearance())
	for _, sc := range append(slices.Clone(theme.Schemes), a.settings.TerminalSchemes...) {
		scheme(sc.ID, sc.Name)
	}
	return out
}

// paletteMatches are the items matching q, the best first, at most
// paletteMax.
func paletteMatches(items []paletteItem, q string) []paletteItem {
	words := splitWords(q)
	type scored struct {
		it    paletteItem
		score int
	}
	var out []scored
	for _, it := range items {
		if s := paletteScore(it, words); s > 0 {
			out = append(out, scored{it, s})
		}
	}
	slices.SortStableFunc(out, func(x, y scored) int { return y.score - x.score })
	n := min(len(out), paletteMax)
	res := make([]paletteItem, n)
	for i := range res {
		res[i] = out[i].it
	}
	return res
}

// splitWords are the words of what is typed, in lower case.
func splitWords(q string) []string { return strings.Fields(strings.ToLower(q)) }

// paletteScore is how well item it matches all the words: more for a
// title starting with a word, then a word of it starting so, then the
// word in it, then in what else finds it, then its letters in order in
// the title; 0 when a word does not match. No word matches everything.
func paletteScore(it paletteItem, words []string) int {
	if len(words) == 0 {
		return 1
	}
	title := strings.ToLower(it.title)
	rest := strings.ToLower(it.sub + " " + it.also)
	total := 0
	for _, w := range words {
		s := 0
		switch {
		case strings.HasPrefix(title, w):
			s = 40
		case wordStarts(title, w):
			s = 30
		case strings.Contains(title, w):
			s = 20
		case strings.Contains(rest, w):
			s = 10
		case inOrder(title, w):
			s = 5
		}
		if s == 0 {
			return 0
		}
		total += s
	}
	return total
}

// wordStarts reports a word of s, after a space or a punctuation mark,
// starting with w.
func wordStarts(s, w string) bool {
	for i := strings.Index(s, w); i >= 0; {
		if i > 0 && strings.ContainsAny(s[i-1:i], " -_/.:(") {
			return true
		}
		j := strings.Index(s[i+1:], w)
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return false
}

// inOrder reports the runes of w in s in order, as "spr" in "split right".
func inOrder(s, w string) bool {
	for _, r := range w {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}

// paletteSnippet sends a snippet to the focused terminal from the palette,
// unlocking the secrets first if they are locked.
func (a *App) paletteSnippet(m locales.Messages, id string, enter bool) {
	if !a.unlocked() {
		a.withSecrets(func() { a.paletteSnippet(m, id, enter) })
		return
	}
	if err := a.sendSnippet(id, enter); err != nil {
		a.toastError(snippetErrorText(m, err))
	}
}

// paletteView is the command palette: a field, and the rows matching
// what it holds. Up and Down choose a row, Enter or a click runs it.
func (a *App) paletteView(c *ui.Context, m locales.Messages) {
	p := &a.palette
	if !p.open {
		return
	}
	tok := currentTokens(c)
	sheet(ui.Modal(c, &p.open, func() {
		if k := a.keyOf(actCommandPalette); c.Shortcut(k.Mods, k.Key) {
			p.open = false
		}
		ui.Column(c).Gap(10).Width(560).Children(func() {
			var items []paletteItem
			ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Icon(c, icons.Search.SVG).Size(16, 16).Shrink(0).TextColor(tok.TextMuted)
				in := formTextInput(c.Key("palette:q"), &p.query).Placeholder(m.PalettePlaceholder()).Label(m.CommandPalette()).
					AutoFocus().Grow(1)
				p.hold.apply(c, p.gen, in)
				in.OnTextKey(func(k ui.TextKey) bool {
					if k.Mods != 0 {
						return false
					}
					switch k.Key {
					case ui.KeyDown:
						p.sel++
					case ui.KeyUp:
						p.sel--
					default:
						return false
					}
					p.reveal = true
					return true
				})
				items = paletteMatches(a.paletteItems(m, tok), p.query)
				if in.Changed() {
					p.sel = 0
				}
				p.sel = max(0, min(p.sel, len(items)-1))
				if in.Submitted() && len(items) > 0 {
					a.runPalette(items[p.sel])
				}
			})
			ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			if len(items) == 0 {
				ui.Text(c, m.PaletteNoResults()).FontSize(theme.Body).TextColor(tok.TextMuted).Padding(theme.Space(2))
				return
			}
			gutterScroll(c, ui.Scroll(c.Key("palette:list")).MaxHeight(420).FillWidth()).Children(func() {
				ui.Column(c).Gap(2).FillWidth().Children(func() {
					for i, it := range items {
						if a.paletteRow(c, tok, it, i == p.sel) {
							a.runPalette(it)
						}
					}
				})
			})
		})
	}), tok)
	p.reveal = false
}

// paletteRow is one row of the palette; it reports a click.
func (a *App) paletteRow(c *ui.Context, tok theme.Tokens, it paletteItem, chosen bool) bool {
	row := ui.ButtonBase(c.Key("palette:"+it.key)).Label(it.title).FillWidth().Padding(6, 10).Gap(10).
		Radius(theme.RadiusControl).AlignItems(ui.Center).Cursor(ui.CursorPointer)
	ic := tok.TextMuted
	switch {
	case chosen:
		row.Background(tok.Press)
		ic = tok.Text
		if a.palette.reveal {
			row.ScrollIntoView()
		}
	case row.Hovered():
		row.Background(tok.Hover)
	}
	row.Children(func() {
		ui.Icon(c, it.icon.SVG).Size(16, 16).Shrink(0).TextColor(ic)
		ui.Column(c).Grow(1).Shrink(1).Gap(1).Children(func() {
			ui.Text(c, it.title).FontSize(theme.Body).FontWeight(500).SingleLine()
			if it.sub != "" {
				ui.Text(c, it.sub).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			}
		})
		if it.on {
			ui.Icon(c, icons.Check.SVG).Size(14, 14).Shrink(0).TextColor(tok.Accent)
		}
		if it.combo.Key != 0 {
			ui.Row(c).Gap(4).Shrink(0).Children(func() {
				for _, cap := range keyCaps(runtime.GOOS, it.combo.Mods, it.combo.Key) {
					keyCap(c, tok, cap)
				}
			})
		}
	})
	return row.Clicked()
}

// runPalette closes the palette and does what item it does.
func (a *App) runPalette(it paletteItem) {
	a.palette.open = false
	it.run()
}

// chooseSetting changes one setting from the palette.
func (a *App) chooseSetting(m locales.Messages, set func(s *settings.Settings)) {
	s := a.settings
	set(&s)
	if err := a.ApplySettings(s); err != nil {
		a.toastError(m.ErrGeneric(err.Error()))
	}
}
