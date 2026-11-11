package app

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type snippetState struct {
	query, group, selected, err string
	// grouped filters the list by group, "" being the default group.
	grouped           bool
	form              *snippetForm
	showing, deleting bool
	deleteID          string
	hold              focusHold
	// inline shows each entry's actions as buttons, the rows being wide
	// enough for them (rowActionsFit); else a More menu holds them.
	inline bool
}

// rowButton is a Small IconButton's width with the gap before it;
// rowTextMin the room a list row keeps for its text beside its actions,
// chosen so a panel's rows show alike with or without the scroll gutter.
const (
	rowButton  = 24 + 2
	rowTextMin = 232
)

// rowActionsFit reports whether a row w wide, of which fixed goes to its
// padding and tile, keeps rowTextMin for its text beside n buttons. A
// row not yet measured does not.
func rowActionsFit(w, fixed float32, n int) bool {
	return w > 0 && w-fixed-float32(n)*rowButton >= rowTextMin
}

// snippetHit is an entry the search matches, with its body as read.
type snippetHit struct {
	n    assets.Snippet
	body string
	err  error
}

func (a *App) snippetTarget() *conn {
	cn := a.focused()
	if cn == nil || cn.kind != layout.Terminal || cn.tv == nil || cn.s == nil || cn.s.Status().State != session.Connected {
		return nil
	}
	return cn
}

// snippetsHead is what the snippets pane's header holds after its name,
// how many there are, and its action, adding one.
func (a *App) snippetsHead(c *ui.Context, m locales.Messages, tok theme.Tokens) (lead func(), acts []barAction) {
	lead = func() {
		if n := len(a.store.Snippets()); n > 0 {
			ui.Text(c, strconv.Itoa(n)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(0)
		}
	}
	return lead, []barAction{{Key: "snippet:new", Button: IconButton{Icon: icons.Add, Label: m.SnippetNew(), Size: Tiny},
		Do: func() { a.openSnippet(m, assets.Snippet{Group: a.snippets.group}, false) }}}
}

// snippetsPanel is the snippets pane under its header: the search and
// the groups over the list, in sections by group, and at the foot the
// terminal they go to.
func (a *App) snippetsPanel(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	all := a.store.Snippets()
	ui.Column(c.Key("snippets")).Fill().Children(func() {
		if len(all) == 0 {
			a.snippetIntro(c, m, tok)
		} else {
			a.snippetList(c, m, tok, all)
		}
		a.snippetFoot(c, m, tok)
	})
}

// snippetIntro takes the place of the list while there are no entries,
// in the middle of the panel; one shorter than it (under other panels)
// cuts its edges rather than run into the foot.
func (a *App) snippetIntro(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	ui.Column(c.Key("snippet:intro")).Grow(1).Shrink(1).MinHeight(0).Clip().FillWidth().Padding(theme.Space(4), theme.Space(8)).Gap(theme.Space(3)).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		softTile(c, icons.API, tok.Accent, 44)
		ui.Column(c).FillWidth().Gap(theme.Space(1)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.SnippetEmptyTitle()).FontSize(theme.Title3).FontWeight(600).TextAlign(ui.Center)
			ui.Text(c, m.SnippetEmptyHint()).FontSize(theme.Footnote).TextColor(tok.TextMuted).TextAlign(ui.Center)
		})
		if pill(c, m.SnippetNew(), true, false, false) {
			a.openSnippet(m, assets.Snippet{}, false)
		}
	})
}

// snippetList is the search, the groups' tabs and the entries matching.
func (a *App) snippetList(c *ui.Context, m locales.Messages, tok theme.Tokens, all []assets.Snippet) {
	var hits []snippetHit
	counts := map[string]int{}
	for _, n := range all {
		body, err := a.snippetContent(n.ID)
		if a.snippetMatches(m, n, body) {
			hits = append(hits, snippetHit{n, body, err})
			counts[n.Group]++
		}
	}
	groups := a.snippetGroups()
	ui.Column(c).FillWidth().Shrink(0).Padding(0, theme.Space(3), theme.Space(2), theme.Space(3)).Gap(theme.Space(2)).Children(func() {
		a.snippetSearch(c, m, tok)
		if a.snippets.err != "" {
			ui.Row(c.Key("snippet:err")).FillWidth().Padding(theme.Space(2), theme.Space(2.5)).Gap(theme.Space(2)).Radius(theme.RadiusControl).
				Background(tok.Danger.Alpha(0.12)).Border(1, tok.Danger.Alpha(0.3)).Children(func() {
				ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(tok.Danger)
				ui.Text(c, a.snippets.err).FontSize(theme.Footnote).Grow(1).Shrink(1)
			})
		}
	})
	if len(groups) > 0 || a.snippets.grouped {
		ui.ScrollHorizontal(c.Key("snippet:groups")).FillWidth().Shrink(0).Role(ui.RoleTabList).Label(m.SnippetFilterGroup()).Children(func() {
			ui.Row(c).Gap(theme.Space(1.5)).Padding(0, theme.Space(3), theme.Space(2), theme.Space(3)).Children(func() {
				a.snippetTab(c, tok, "all", m.SnippetAll(), len(hits), !a.snippets.grouped, func() { a.snippets.grouped, a.snippets.group = false, "" })
				for _, g := range append([]string{""}, groups...) {
					a.snippetTab(c, tok, "g:"+g, snippetGroupTitle(m, g), counts[g], a.snippets.grouped && a.snippets.group == g, func() { a.snippets.grouped, a.snippets.group = true, g })
				}
			})
		})
	}
	ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
	gutterScroll(c, ui.Scroll(c.Key("snippet:list")).Grow(1).Shrink(1).MinHeight(0).FillWidth()).Children(func() {
		ui.Column(c).FillWidth().Padding(theme.Space(2)).Gap(theme.Space(0.5)).Children(func() {
			// In sections by group while all of them show, and there are any.
			sections := !a.snippets.grouped && len(groups) > 0
			shown, last := 0, "\x00"
			for _, h := range hits {
				if a.snippets.grouped && h.n.Group != a.snippets.group {
					continue
				}
				if sections && h.n.Group != last {
					last = h.n.Group
					a.snippetSection(c, m, tok, last, counts[last], shown == 0)
				}
				shown++
				a.snippetRow(c, m, tok, h.n, h.body, h.err)
			}
			if shown == 0 {
				ui.Column(c).FillWidth().Padding(theme.Space(8), theme.Space(4)).AlignItems(ui.Center).Children(func() {
					panelNote(c, tok, m.SnippetEmpty())
				})
			}
		})
	})
}

// snippetSearch is the search field of the snippets.
func (a *App) snippetSearch(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	searchField(c, m, tok, "snippet:search", &a.snippets.query, m.SnippetSearch())
}

// searchField is a panel's search field, as the hosts' one: a magnifier,
// the text and, while it holds any, an IconButton clearing it, as Escape
// does. It returns the text's field.
func searchField(c *ui.Context, m locales.Messages, tok theme.Tokens, key string, q *string, placeholder string) ui.Element {
	var in ui.Element
	ui.Row(c.Key(key)).FillWidth().Height(30).Padding(0, theme.Space(1), 0, theme.Space(2.5)).Gap(theme.Space(1.5)).AlignItems(ui.Center).
		Radius(theme.RadiusControl).Background(tok.Fill).Children(func() {
		ui.Icon(c, icons.Search.SVG).Size(14, 14).Shrink(0).TextColor(tok.TextMuted)
		in = ui.TextInputBase(c.Key("q"), q).Placeholder(placeholder).Label(placeholder).Grow(1).Shrink(1).MinWidth(40)
		if *q != "" {
			if (IconButton{Icon: icons.ClearText, Label: m.ClearSearch(), Size: Small}).Clicked(c, "clear") || in.Shortcut(0, ui.KeyEscape) {
				*q = ""
				in.Focus()
			}
		} else {
			// as high as with the button, so that the field keeps its size
			ui.Box(c).Size(24, 24).Shrink(0)
		}
	})
	return in
}

// snippetTab is a capsule choosing a group, or all of them, with how many
// of its entries match the search.
func (a *App) snippetTab(c *ui.Context, tok theme.Tokens, key, label string, count int, on bool, choose func()) {
	b := ui.ButtonBase(c.Key("snippet:tab:"+key)).Label(label).Role(ui.RoleTab).Selected(on).Padding(theme.Space(1), theme.Space(2.5)).
		Gap(theme.Space(1.5)).Radius(theme.Capsule).AlignItems(ui.Center).Shrink(0).Cursor(ui.CursorPointer)
	// Chosen, it is raised as a segmented control's choice is.
	fg := tok.TextMuted
	switch {
	case on:
		theme.Shadow(tok.Card).On(b.Background(tok.Inset))
		fg = tok.Text
	case b.Pressed() || b.Hovered():
		b.Background(tok.Press)
	default:
		b.Background(tok.Fill)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(theme.Footnote).FontWeight(500).TextColor(fg).SingleLine().MaxWidth(chipLabelWidth)
		ui.Text(c, strconv.Itoa(count)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
	})
	if b.Clicked() {
		choose()
	}
}

// accentWash is how much accent fills what is chosen, more in the dark.
func accentWash(tok theme.Tokens) float32 {
	if tok.Dark {
		return 0.22
	}
	return 0.12
}

// snippetSection heads the entries of a group in the list.
func (a *App) snippetSection(c *ui.Context, m locales.Messages, tok theme.Tokens, g string, count int, first bool) {
	top := theme.Space(3)
	if first {
		top = theme.Space(1)
	}
	ui.Row(c.Key("snippet:sec:"+g)).FillWidth().Padding(top, theme.Space(2), theme.Space(1), theme.Space(2)).Gap(theme.Space(1.5)).AlignItems(ui.Center).Children(func() {
		dot(c, snippetTint(tok, g))
		ui.Text(c, snippetGroupTitle(m, g)).FontSize(theme.Caption).FontWeight(600).TextColor(tok.TextMuted).Shrink(1).SingleLine()
		ui.Text(c, strconv.Itoa(count)).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
	})
}

// snippetTint is a group's identity tint, the accent for the default one.
func snippetTint(tok theme.Tokens, g string) ui.Color {
	if g == "" {
		return tok.Accent
	}
	return tok.Tint(g)
}

// snippetFoot is the panel's foot: the terminal the entries go to, if any.
func (a *App) snippetFoot(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	ui.Box(c).Height(1).Shrink(0).FillWidth().Background(tok.Line)
	ui.Row(c.Key("snippet:foot")).Height(statusBarHeight).Shrink(0).Padding(0, theme.Space(4)).Gap(theme.Space(2)).AlignItems(ui.Center).Children(func() {
		col, text := tok.TextMuted, m.SnippetNoTarget()
		if cn := a.snippetTarget(); cn != nil {
			col, text = tok.Success, m.SnippetTarget(a.pv(cn.host.Label()), strconv.Itoa(cn.termNo))
		}
		dot(c, col)
		ui.Text(c, text).FontSize(theme.Caption).TextColor(tok.TextMuted).Shrink(1).SingleLine()
	})
}

// snippetGroups returns the named groups in use; the default group, stored
// as "", always exists and is not among them.
func (a *App) snippetGroups() []string {
	out := []string{}
	seen := map[string]bool{}
	for _, n := range a.store.Snippets() {
		if n.Group != "" && !seen[n.Group] && snippetGroupName(n.Group) != "" {
			out = append(out, n.Group)
			seen[n.Group] = true
		}
	}
	return out
}

// snippetGroupTitle names a group, "" being the default group.
func snippetGroupTitle(m locales.Messages, g string) string {
	if g == "" {
		return m.SnippetDefaultGroup()
	}
	return g
}

// snippetGroupName maps the default group's name, in any language, to the
// stored "", so that the choice survives a change of language.
func snippetGroupName(g string) string {
	g = strings.TrimSpace(g)
	for _, l := range settings.Languages {
		if strings.EqualFold(g, locales.In(l).SnippetDefaultGroup()) {
			return ""
		}
	}
	return g
}

// snippetMatches reports whether the search matches an entry, by name,
// group or body; the group chosen filters apart.
func (a *App) snippetMatches(m locales.Messages, n assets.Snippet, body string) bool {
	return strings.Contains(strings.ToLower(n.Name+"\n"+snippetGroupTitle(m, n.Group)+"\n"+body), strings.ToLower(strings.TrimSpace(a.snippets.query)))
}

// snippetRow is an entry: its tile, name and first line, with its action
// and, as wide as the panel allows, its other actions or a More menu of
// them. Chosen, it opens on the whole body, its group and its mode; only
// its action sends it, never a click on the row.
func (a *App) snippetRow(c *ui.Context, m locales.Messages, tok theme.Tokens, n assets.Snippet, body string, err error) {
	sel := a.snippets.selected == n.ID
	label, ic, mode := m.SnippetInsertAction(n.Name), icons.Paste, m.SnippetInsert()
	if n.SendEnter {
		label, ic, mode = m.SnippetSendAction(n.Name), icons.Send, m.SnippetSendEnter()
	}
	target := a.snippetTarget()
	disabled := (err != nil && !errors.Is(err, keychain.ErrLocked)) || target == nil || target.tv.snippetSending
	preview := strings.SplitN(body, "\n", 2)[0]
	if err != nil {
		body = snippetErrorText(m, err)
		preview = body
	}
	more := "snippet:more:" + n.ID
	box := ui.Column(c.Key("snippet:row:" + n.ID)).FillWidth().Radius(theme.RadiusControl)
	// Measured with the most actions an entry has, so all rows show alike.
	if fit := rowActionsFit(box.Bounds().W, theme.Space(1)+theme.Space(4)+28+theme.Space(2.5), 5); fit != a.snippets.inline {
		a.snippets.inline = fit
		c.Invalidate()
	}
	switch {
	case sel:
		box.Background(tok.Accent.Alpha(accentWash(tok)))
	case box.Hovered() || a.menu.key == more:
		box.Background(tok.Hover)
	}
	box.Children(func() {
		ui.Row(c).FillWidth().Padding(0, theme.Space(1), 0, 0).Gap(theme.Space(0.5)).AlignItems(ui.Center).Children(func() {
			row := ui.ButtonBase(c.Key("snippet:select:" + n.ID)).Label(m.SnippetPreview(n.Name)).Selected(sel).Grow(1).Shrink(1).MinWidth(0).
				Padding(theme.Space(2)).Gap(theme.Space(2.5)).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer)
			// The row around it shows the choice, in place of the list's fill.
			row.Background(tok.Accent.Alpha(0)).TextColor(tok.Text)
			row.Children(func() {
				softTile(c, icons.API, snippetTint(tok, n.Group), 28)
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(theme.Space(0.5)).Children(func() {
					ui.Text(c, n.Name).FontSize(theme.Body).FontWeight(600).SingleLine()
					// Chosen, the whole body shows below instead.
					if !sel {
						tx := ui.Text(c, preview).Font(a.termFamily()).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
						if err != nil {
							tx.TextColor(tok.Warning)
						}
					}
				})
			})
			if row.Clicked() {
				a.withSecrets(func() { a.snippets.selected = n.ID })
			}
			if (IconButton{Icon: ic, Label: label, Disabled: disabled, Size: Small}).Clicked(c, "snippet:send:"+n.ID) {
				a.startSnippet(m, n.ID, n.SendEnter)
			}
			if a.snippets.inline {
				a.snippetActions(c, m, n, disabled)
			} else {
				a.menuButton(c, more, IconButton{Icon: icons.More, Label: m.SnippetMore(n.Name), Size: Small}, func() []menuEntry { return a.snippetMenu(m, n, disabled) })
			}
		})
		if !sel {
			return
		}
		// Under the name, past the tile.
		ui.Column(c.Key("snippet:detail")).FillWidth().Padding(0, theme.Space(3), theme.Space(3), theme.Space(11.5)).Gap(theme.Space(2)).Children(func() {
			ui.Scroll(c.Key("snippet:body")).FillWidth().MaxHeight(theme.Space(40)).Radius(theme.RadiusSmall).Background(a.contentSurfaceFill(tok.Inset)).Border(1, tok.Line).Children(func() {
				ui.Column(c).FillWidth().Padding(theme.Space(2), theme.Space(2.5)).Children(func() {
					ui.Text(c, body).Font(a.termFamily()).FontSize(theme.Footnote)
				})
			})
			ui.Row(c).FillWidth().Gap(theme.Space(1.5)).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Folder.SVG).Size(12, 12).Shrink(0).TextColor(tok.TextMuted)
				ui.Text(c, snippetGroupTitle(m, n.Group)).FontSize(theme.Caption).TextColor(tok.TextMuted).Shrink(1).SingleLine()
				ui.Box(c).Size(3, 3).Shrink(0).Radius(1.5).Background(tok.TextMuted)
				ui.Icon(c, ic.SVG).Size(12, 12).Shrink(0).TextColor(tok.TextMuted)
				ui.Text(c, mode).FontSize(theme.Caption).TextColor(tok.TextMuted).Shrink(1).SingleLine()
			})
		})
	})
	a.contextMenu(box, func() []menuEntry { return a.snippetMenu(m, n, disabled) })
}

// snippetActions are an entry's actions beside its own, each an
// IconButton: inserting one that sends Enter, editing, duplicating and
// deleting it. Its More menu and the right click list them too
// (snippetMenu).
func (a *App) snippetActions(c *ui.Context, m locales.Messages, n assets.Snippet, disabled bool) {
	if n.SendEnter {
		if (IconButton{Icon: icons.Paste, Label: m.SnippetInsertAction(n.Name), Disabled: disabled, Size: Small}).Clicked(c, "snippet:insert:"+n.ID) {
			a.startSnippet(m, n.ID, false)
		}
	}
	if (IconButton{Icon: icons.Edit, Label: m.SnippetEditAction(n.Name), Size: Small}).Clicked(c, "snippet:edit:"+n.ID) {
		a.openSnippet(m, n, false)
	}
	if (IconButton{Icon: icons.Copy, Label: m.SnippetDuplicateAction(n.Name), Size: Small}).Clicked(c, "snippet:dup:"+n.ID) {
		a.openSnippet(m, n, true)
	}
	if (IconButton{Icon: icons.Delete, Label: m.SnippetDeleteAction(n.Name), Danger: true, Size: Small}).Clicked(c, "snippet:delete:"+n.ID) {
		a.snippets.deleteID, a.snippets.deleting = n.ID, true
	}
}

// snippetMenu is an entry's actions beside its own, in snippetActions'
// order.
func (a *App) snippetMenu(m locales.Messages, n assets.Snippet, disabled bool) []menuEntry {
	var out []menuEntry
	if n.SendEnter {
		out = append(out, menuEntry{Icon: icons.Paste, Label: m.SnippetInsert(), Disabled: disabled, Do: func() { a.startSnippet(m, n.ID, false) }}, menuSep)
	}
	return append(out, menuEntry{Icon: icons.Edit, Label: m.Edit(), Do: func() { a.openSnippet(m, n, false) }}, menuEntry{Icon: icons.Copy, Label: m.SnippetDuplicate(), Do: func() { a.openSnippet(m, n, true) }}, menuEntry{Icon: icons.Delete, Label: m.Delete(), Danger: true, Do: func() { a.snippets.deleteID = n.ID; a.snippets.deleting = true }})
}
func (a *App) startSnippet(m locales.Messages, id string, enter bool) {
	if !a.unlocked() {
		a.withSecrets(func() { a.startSnippet(m, id, enter) })
		return
	}

	a.snippets.err = ""
	if err := a.sendSnippet(id, enter); err != nil {
		a.snippets.err = snippetErrorText(m, err)
	}
}
func (a *App) snippetSendResult(err error) {
	a.toast(func(m locales.Messages) ui.Toast {
		if err != nil {
			return ui.Toast{Title: snippetErrorText(m, err), Type: "error"}
		}
		return ui.Toast{Title: m.SnippetSent()}
	})
}
