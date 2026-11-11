package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// termPalette is the terminal's colors: the chosen scheme's, or the app's
// own for the appearance.
func (a *App) termPalette(tok theme.Tokens) theme.Terminal {
	if sc, ok := theme.FindScheme(a.settings.TerminalScheme, a.settings.TerminalSchemes); ok {
		return sc.Terminal(tok.Terminal)
	}
	return tok.Terminal
}

// schemeSection chooses the terminal's color scheme among the app's own
// colors, the built-in schemes and the imported ones, and imports more.
func (a *App) schemeSection(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	a.settingsSection(c, tok, icons.Appearance, m.ColorScheme(), func(g *settingsGroup) {
		g.row(m.TerminalStripe(), m.TerminalStripeNote(), func() {
			on := s.TerminalStripe
			if formSwitch(c.Key("termstripe"), &on).Label(m.TerminalStripe()).Changed() {
				s.TerminalStripe, *changed = on, true
			}
		})
		g.divider()
		grid := ui.Row(c.Key("schemes")).Wrap().Gap(10).Padding(14).FillWidth()
		if grid.FileDragOver() {
			grid.Background(tok.Accent.Alpha(0.08))
		}
		for _, p := range grid.DroppedFiles() {
			data, err := readSchemeFile(p)
			a.addScheme(m, p, data, err)
		}
		grid.Children(func() {
			card := func(id, name string, pal theme.Terminal) {
				if schemeCard(c, tok, a.termFamily(), id, name, pal, s.TerminalScheme == id) {
					s.TerminalScheme, *changed = id, true
				}
			}
			card("", m.SchemeFollowAppearance(), tok.Terminal)
			for _, sc := range append(slices.Clone(theme.Schemes), s.TerminalSchemes...) {
				card(sc.ID, sc.Name, sc.Terminal(tok.Terminal))
			}
		})
		g.divider()
		g.row(m.SchemeImport(), m.SchemeImportNote(), func() {
			if pill(c, m.SchemeImportButton(), false, false, a.p.PickFile == nil) {
				a.pickScheme(m)
			}
		})
		for _, sc := range s.TerminalSchemes {
			g.row(sc.Name, m.SchemeImported(), func() {
				if (IconButton{Icon: icons.Delete, Label: m.SchemeDelete(), Danger: true, Size: Small}).Clicked(c, "scheme:del:"+sc.ID) {
					s.RemoveTerminalScheme(sc.ID)
					*changed = true
				}
			})
		}
	})
}

// schemeCard is one scheme to choose: a little terminal in its colors and
// its name; it reports a click.
func schemeCard(c *ui.Context, tok theme.Tokens, font string, id, name string, pal theme.Terminal, on bool) bool {
	b := ui.ButtonBase(c.Key("scheme:" + id)).Label(name).Tooltip(name).Checked(on).Width(148).Padding(4).
		Radius(theme.RadiusCard).Cursor(ui.CursorPointer)
	switch {
	case on:
		b.Border(2, tok.Accent)
	case b.Hovered():
		b.Border(2, tok.Text.Alpha(0.25))
	default:
		b.Border(2, ui.Transparent)
	}
	b.Children(func() {
		ui.Column(c).Gap(6).FillWidth().Children(func() {
			ui.Column(c).Gap(6).Padding(8, 10).FillWidth().Radius(theme.RadiusControl).Background(pal.Background).
				Border(1, tok.Line).Children(func() {
				ui.RichText(c, ui.Span{Text: "~ ", Color: pal.ANSI[4], Font: font},
					ui.Span{Text: "$ ls", Color: pal.Foreground, Font: font},
					ui.Span{Text: "▍", Color: pal.Cursor, Font: font}).FontSize(theme.Footnote).SingleLine()
				ui.Row(c).Gap(4).Children(func() {
					for i := 1; i <= 6; i++ {
						ui.Box(c).Size(12, 12).Radius(theme.Capsule).Background(pal.ANSI[i])
					}
				})
			})
			ui.Text(c, name).FontSize(theme.Footnote).FontWeight(500).SingleLine().Padding(0, 4)
		})
	})
	return b.Clicked()
}

// readSchemeFile reads a color scheme file, refusing one too large to be.
func readSchemeFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() > theme.MaxITermColors {
		return nil, theme.ErrNotITermColors
	}
	return os.ReadFile(path)
}

// pickScheme imports a scheme through the system's open dialog, off the
// UI thread.
func (a *App) pickScheme(m locales.Messages) {
	title := m.SchemeChooseFile()
	go func() {
		path, err := a.p.PickFile(title)
		if path == "" && err == nil {
			return
		}
		var data []byte
		if err == nil {
			data, err = readSchemeFile(path)
		}
		a.post(func() { a.addScheme(m, path, data, err) })
	}()
}

// addScheme imports the scheme read from path, named after the file, and
// chooses it; one of the same name is replaced.
func (a *App) addScheme(m locales.Messages, path string, data []byte, err error) {
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	if err != nil && err != theme.ErrNotITermColors {
		a.toastError(m.ErrReadFile(base, err.Error()))
		return
	}
	sc, err := theme.ParseITermColors(name, data)
	if err != nil {
		a.toastError(m.ErrSchemeImport(base))
		return
	}
	s := a.settings
	s.AddTerminalScheme(sc)
	if err := a.ApplySettings(s); err != nil {
		a.toastError(m.ErrGeneric(err.Error()))
	}
}
