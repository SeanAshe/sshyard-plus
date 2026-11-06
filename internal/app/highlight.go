package app

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/highlight"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The terminal colors the keywords of its output (internal/highlight) as
// the Terminal settings say: the built-in rules on and the user's own,
// which the rule sheet edits.

// highlighter is the Highlighter of settings.Highlight, nil while it is
// off.
func (a *App) highlighter() *highlight.Highlighter {
	h := a.settings.Highlight
	if !h.On {
		return nil
	}
	if a.hl == nil {
		a.hl = highlight.New(h.Rules, h.PresetsOn())
	}
	return a.hl
}

// presetName names a built-in rule.
func presetName(m locales.Messages, id string) string {
	switch id {
	case "errors":
		return m.HighlightErrors()
	case "warnings":
		return m.HighlightWarnings()
	case "info":
		return m.HighlightInfo()
	case "success":
		return m.HighlightSuccess()
	case "network":
		return m.HighlightNetwork()
	case "urls":
		return m.HighlightURLs()
	case "datetime":
		return m.HighlightDateTime()
	case "http":
		return m.HighlightHTTP()
	case "ids":
		return m.HighlightIDs()
	case "shell":
		return m.HighlightShell()
	}
	return id
}

// hlColors are the terminal's colors a rule may show in: those that read
// on its background, as black, white and gray may not.
var hlColors = []uint8{1, 2, 3, 4, 5, 6, 9, 10, 11, 12, 13, 14}

// hlColorName names a color of hlColors.
func hlColorName(m locales.Messages, i uint8) string {
	names := []string{m.ColorRed(), m.ColorGreen(), m.ColorYellow(), m.ColorBlue(), m.ColorMagenta(), m.ColorCyan()}
	n := int(i%8) - 1
	if n < 0 || n >= len(names) {
		return strconv.Itoa(int(i))
	}
	if i >= 8 {
		return m.ColorBright(names[n])
	}
	return names[n]
}

// hlSpans is text as h shows it in the terminal of palette pal.
func hlSpans(h *highlight.Highlighter, text string, pal theme.Terminal) []ui.Span {
	var out []ui.Span
	last := 0
	if !h.Empty() {
		for _, s := range h.Spans(text) {
			if s.From > last {
				out = append(out, ui.Span{Text: text[last:s.From]})
			}
			out = append(out, hlSpan(text[s.From:s.To], s.Style, pal))
			last = s.To
		}
	}
	if last < len(text) {
		out = append(out, ui.Span{Text: text[last:]})
	}
	return out
}

// hlSpan is text in style s, as the terminal draws it: bold brightens the
// first eight colors of the text.
func hlSpan(text string, s highlight.Style, pal theme.Terminal) ui.Span {
	sp := ui.Span{Text: text, Underline: s.Underline}
	switch i := s.Color % 16; {
	case s.Background:
		sp.Background = pal.ANSI[i]
	case s.Bold && i < 8:
		sp.Color = pal.ANSI[i+8]
	default:
		sp.Color = pal.ANSI[i]
	}
	if s.Bold {
		sp.Weight = 700
	}
	return sp
}

// hlSample is the output the settings preview the highlighting on.
var hlSample = []string{
	"2026-11-05 09:30:01 INFO  listening on https://0.0.0.0:8443",
	"2026-11-05 09:30:02 WARN  retrying 10.0.0.12:5432 after timeout",
	"2026-11-05 09:30:03 ERROR connection refused (status=503)",
	`"GET /api/items HTTP/1.1" 200 id=3f2b8c1e-9d4a-4b6f-8e2a-1c5d7f9b0a3e OK`,
}

// hlPreview draws lines in the terminal's font and colors, as h shows
// them.
func (a *App) hlPreview(c *ui.Context, tok theme.Tokens, key string, h *highlight.Highlighter, lines []string) {
	pal := a.termPalette(tok)
	fam := a.termFamily()
	ui.Column(c.Key(key)).FillWidth().Padding(12, 14).Gap(2).Radius(theme.RadiusControl).
		Background(pal.Background).TextColor(pal.Foreground).Border(1, tok.Line).Children(func() {
		for _, l := range lines {
			spans := hlSpans(h, l, pal)
			for i := range spans {
				spans[i].Font = fam
			}
			ui.RichText(c, spans...).FontSize(theme.Footnote)
		}
	})
}

// highlightSection is the Terminal settings' keyword highlighting: on or
// off, how far it goes, the built-in rules and the user's own.
func (a *App) highlightSection(c *ui.Context, m locales.Messages, tok theme.Tokens, s *settings.Settings, changed *bool) {
	h := &s.Highlight
	a.settingsSection(c, tok, icons.Highlight, tok.TintAt(5), m.HighlightSection(), func(g *settingsGroup) {
		toggle := func(key, label, note string, v *bool) {
			g.row(label, note, func() {
				on := *v
				if formSwitch(c.Key(key), &on).Label(label).Changed() {
					*v, *changed = on, true
				}
			})
		}
		toggle("hl:on", m.HighlightOn(), m.HighlightOnNote(), &h.On)
		if !h.On {
			return
		}
		toggle("hl:override", m.HighlightOverride(), m.HighlightOverrideNote(), &h.Override)
		toggle("hl:full", m.HighlightFullScreen(), m.HighlightFullScreenNote(), &h.FullScreen)
		g.divider()
		ui.Column(c).FillWidth().Padding(10, 14, 12, 14).Gap(6).Children(func() {
			ui.Text(c, m.HighlightPreview()).FontSize(theme.Caption).TextColor(tok.TextMuted)
			a.hlPreview(c, tok, "hl:preview", a.highlighter(), hlSample)
		})
	})
	if !h.On {
		return
	}
	a.settingsSection(c, tok, icons.Logs, tok.TintAt(2), m.HighlightPresets(), func(g *settingsGroup) {
		for _, p := range highlight.Presets {
			name := presetName(m, p.ID)
			g.row(name, p.Sample, func() {
				on := !slices.Contains(h.PresetsOff, p.ID)
				if formSwitch(c.Key("hl:preset:"+p.ID), &on).Label(name).Changed() {
					// A new slice: the one held is the settings' in force.
					off := slices.DeleteFunc(slices.Clone(h.PresetsOff), func(id string) bool { return id == p.ID })
					if !on {
						off = append(off, p.ID)
					}
					h.PresetsOff, *changed = off, true
				}
			})
		}
	})
	pal := a.termPalette(tok)
	a.settingsSection(c, tok, icons.Edit, tok.TintAt(4), m.HighlightRules(), func(g *settingsGroup) {
		if len(h.Rules) == 0 {
			g.row(m.HighlightNoRules(), "", func() {})
		}
		for i, r := range h.Rules {
			key := "hl:rule:" + strconv.Itoa(i)
			swatch := func() {
				ui.Box(c).Size(12, 12).Shrink(0).Radius(theme.Capsule).Background(pal.ANSI[r.Color%16])
			}
			g.row(r.Pattern, hlRuleNote(m, r), func() {
				on := !r.Off
				if formSwitch(c.Key(key+":on"), &on).Label(m.HighlightUse()).Changed() {
					rules := slices.Clone(h.Rules)
					rules[i].Off = !on
					h.Rules, *changed = rules, true
				}
				if (IconButton{Icon: icons.Edit, Label: m.HighlightEdit(), Size: Small}).Clicked(c, key+":edit") {
					a.openHighlightRule(i)
				}
				if (IconButton{Icon: icons.Delete, Label: m.HighlightDelete(), Danger: true, Size: Small}).Clicked(c, key+":del") {
					h.Rules, *changed = slices.Delete(slices.Clone(h.Rules), i, i+1), true
				}
			}, swatch)
		}
		g.row("", "", func() {
			if pill(c, m.HighlightAdd(), false, false, len(h.Rules) >= highlight.MaxRules) {
				a.openHighlightRule(-1)
			}
		})
	})
}

// hlRuleNote says how a rule matches.
func hlRuleNote(m locales.Messages, r highlight.Rule) string {
	var parts []string
	if r.Regex {
		parts = append(parts, m.HighlightRegex())
	}
	if r.CaseSensitive {
		parts = append(parts, m.HighlightCase())
	}
	if r.WholeWord {
		parts = append(parts, m.HighlightWord())
	}
	return strings.Join(parts, " · ")
}

// hlRuleForm is the rule sheet's draft: of rule index of the settings, or
// a new one for -1.
type hlRuleForm struct {
	index  int
	rule   highlight.Rule
	sample string
	err    string
}

// openHighlightRule opens the rule sheet on rule i, or on a new rule.
func (a *App) openHighlightRule(i int) {
	f := &hlRuleForm{index: -1, rule: highlight.Rule{Style: highlight.Style{Color: hlColors[0]}}}
	if rules := a.settings.Highlight.Rules; i >= 0 && i < len(rules) {
		f.index, f.rule = i, rules[i]
	}
	a.hlForm, a.hlEditing = f, true
}

// saveHighlightRule saves the rule sheet's rule, or says why it cannot.
func (a *App) saveHighlightRule(m locales.Messages) {
	f := a.hlForm
	if f == nil {
		return
	}
	r := f.rule
	if strings.TrimSpace(r.Pattern) == "" {
		f.err = m.HighlightEmpty()
		return
	}
	if _, err := r.Compile(); err != nil {
		f.err = m.HighlightBadRegex(err.Error())
		return
	}
	s := a.Settings()
	rules := slices.Clone(s.Highlight.Rules)
	if f.index >= 0 && f.index < len(rules) {
		rules[f.index] = r
	} else {
		rules = append(rules, r)
	}
	s.Highlight.Rules = rules
	if err := a.ApplySettings(s); err != nil {
		f.err = m.ErrGeneric(err.Error())
		return
	}
	a.hlEditing = false
}

// highlightView is the rule sheet: the pattern and how it matches, the
// style of its matches, and a sample to try it on.
func (a *App) highlightView(c *ui.Context, m locales.Messages) {
	f := a.hlForm
	if f == nil {
		return
	}
	tok := currentTokens(c)
	r := &f.rule
	sheet(stickyModal(c, &a.hlEditing, func() {
		ui.Column(c).Gap(16).Width(440).Children(func() {
			title := m.HighlightAdd()
			if f.index >= 0 {
				title = m.HighlightEdit()
			}
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				tile(c, icons.Highlight, theme.Brand, 36)
				sheetTitle(c, title)
			})
			ui.Form(c, func() {
				ui.Field(c, m.HighlightPattern(), func() {
					in := formTextInput(c.Key("hl:f:pattern"), &r.Pattern).Placeholder(m.HighlightPatternPlaceholder()).AutoFocus()
					a.hlHold.apply(c, f, in)
					if in.Changed() {
						f.err = ""
					}
					if in.Submitted() {
						a.saveHighlightRule(m)
					}
				})
			})
			flag := func(key, label string, v *bool) {
				ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
					ui.Text(c, label).FontSize(theme.Body).Grow(1)
					if formSwitch(c.Key(key), v).Label(label).Changed() {
						f.err = ""
					}
				})
			}
			ui.Column(c).Gap(8).FillWidth().Children(func() {
				flag("hl:f:regex", m.HighlightRegex(), &r.Regex)
				flag("hl:f:case", m.HighlightCase(), &r.CaseSensitive)
				flag("hl:f:word", m.HighlightWord(), &r.WholeWord)
			})
			pal := a.termPalette(tok)
			ui.Column(c).Gap(8).FillWidth().Children(func() {
				ui.Text(c, m.HighlightColor()).FontSize(theme.Body)
				ui.Row(c.Key("hl:f:colors")).Wrap().Gap(6).AlignItems(ui.Center).FillWidth().Children(func() {
					for _, i := range hlColors {
						name := hlColorName(m, i)
						on := r.Color == i
						sw := ui.ButtonBase(c.Key("hl:f:color:"+strconv.Itoa(int(i)))).Label(name).Tooltip(name).Size(22, 22).Radius(theme.Capsule).
							Background(pal.ANSI[i]).Cursor(ui.CursorPointer).Checked(on)
						if on {
							sw.Border(2, tok.Text.Alpha(0.75))
						} else if sw.Hovered() {
							sw.Border(2, tok.Text.Alpha(0.25))
						}
						if sw.Clicked() {
							r.Color = i
						}
					}
				})
				flag("hl:f:bg", m.HighlightBackground(), &r.Background)
				flag("hl:f:bold", m.HighlightBold(), &r.Bold)
				flag("hl:f:underline", m.HighlightUnderline(), &r.Underline)
			})
			ui.Column(c).Gap(6).FillWidth().Children(func() {
				formTextInput(c.Key("hl:f:sample"), &f.sample).Placeholder(m.HighlightSample()).Label(m.HighlightSample()).FillWidth()
				sample := f.sample
				if sample == "" {
					sample = r.Pattern
				}
				try := *r
				try.Off = false
				a.hlPreview(c, tok, "hl:f:preview", highlight.New([]highlight.Rule{try}, nil), []string{sample})
			})
			if f.err != "" {
				ui.Text(c, f.err).FontSize(theme.Footnote).TextColor(tok.Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Cancel(), false, false, false) {
					a.hlEditing = false
				}
				if pill(c, m.Save(), true, false, false) {
					a.saveHighlightRule(m)
				}
			})
		})
	}), tok)
	if !a.hlEditing {
		a.hlForm = nil
	}
}
