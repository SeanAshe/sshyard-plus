package app

import (
	"slices"
	"strings"
	"testing"

	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

func TestSettingsMatches(t *testing.T) {
	en := locales.In("en")
	if got := settingsMatches(en, "  "); got != nil {
		t.Fatalf("an empty search found %v", got)
	}
	if got := settingsMatches(en, "zzzz nothing"); len(got) != 0 {
		t.Fatalf("nonsense found %v", got)
	}

	// A setting is found on its page, by its label in English too.
	for _, m := range []locales.Messages{en, locales.In("zh-Hans")} {
		got := settingsMatches(m, "keep alive")
		if len(got) == 0 || got[0].item.page != prefTerminal || got[0].hits[0].label != m.KeepAlive() {
			t.Fatalf("keep alive found %+v", got)
		}
	}

	// By its note as well.
	got := settingsMatches(en, strings.Fields(en.CopyOnSelectNote())[0])
	if !slices.ContainsFunc(flatHits(got), func(h settingHit) bool { return h.label == en.CopyOnSelect() }) {
		t.Fatalf("the note does not find its setting: %+v", got)
	}

	// A page matching goes before its settings, its page first of all.
	got = settingsMatches(en, "terminal")
	if len(got) == 0 || got[0].item.page != prefTerminal || got[0].hits[0] != (settingHit{page: prefTerminal}) || len(got[0].hits) < 2 {
		t.Fatalf("terminal found %+v", got)
	}

	// Shortcuts are found on their page.
	got = settingsMatches(en, en.SplitRight())
	if !slices.ContainsFunc(flatHits(got), func(h settingHit) bool { return h.page == prefShortcuts && h.label == en.SplitRight() }) {
		t.Fatalf("split right found %+v", got)
	}
}

func TestSettingsSearchOpensSetting(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	a.openSettings(prefGeneral)
	tt.Frame()

	// Find focuses the field; typing searches and Enter opens the match.
	a.runAction(actFind)
	tt.Frame()
	if active, _ := tt.TextInputActive(); !active || a.prefSearch.focus {
		t.Fatalf("find did not focus the search (active %v)", active)
	}
	tt.Type("keep alive")
	tt.Frame()
	if a.prefSearch.query != "keep alive" || !tt.HasText(m.KeepAlive()) {
		t.Fatalf("query %q; texts %q", a.prefSearch.query, tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	ps := a.prefSearch
	if a.prefNow() != prefTerminal || ps.query != "" || ps.row != "" || ps.flash != m.KeepAlive() || ps.flashTill.IsZero() {
		t.Fatalf("page %q, search %+v", a.prefNow(), ps)
	}

	// A row the page does not draw is not waited for.
	a.openSetting(prefFiles, "no such row")
	tt.Frame()
	if a.prefNow() != prefFiles || a.prefSearch.row != "" || a.prefSearch.flash != "" {
		t.Fatalf("page %q, search %+v", a.prefNow(), a.prefSearch)
	}

	// A page in the sidebar opens and empties the search.
	a.prefSearch.query = "font"
	a.openSetting(prefAbout, "")
	if a.prefNow() != prefAbout || a.prefSearch.query != "" {
		t.Fatalf("page %q, query %q", a.prefNow(), a.prefSearch.query)
	}

	// Outside the settings, Find does not reach for their search.
	a.closeSettings()
	tt.Frame()
	a.runAction(actFind)
	if a.prefSearch.focus {
		t.Fatal("find focused the settings' search outside the settings")
	}
}

// TestSettingsIndexCoversPages draws every page and checks that the
// index lists each row and section on it, and that each setting listed is
// on its page.
func TestSettingsIndexCoversPages(t *testing.T) {
	a, tt := newTestApp(t)
	m := locales.In("en")
	var drawn []string
	old := settingsRowDrawn
	t.Cleanup(func() { settingsRowDrawn = old })
	settingsRowDrawn = func(label string) { drawn = append(drawn, label) }

	indexed := map[prefPage]map[string]bool{}
	for _, e := range settingsIndex() {
		if indexed[e.page] == nil {
			indexed[e.page] = map[string]bool{}
		}
		indexed[e.page][e.label(m)] = true
	}
	// What a page shows of the user's data or of how things stand, rather
	// than a setting.
	status := map[string]bool{"": true, m.NoCredentials(): true, m.NoProxies(): true, m.NoBackupTargets(): true,
		m.HighlightNoRules(): true, m.UpdatesDisabled(): true}
	for _, p := range a.settings.AIProviders {
		status[providerLabel(p)] = true
	}
	for _, st := range a.skillTargets() {
		status[st.name] = true
	}
	// What a page shows only in a state the test does not set up.
	conditional := map[string]bool{}
	for _, l := range []string{m.DefaultShell(), m.FieldPort(), m.ApiAddress(), m.McpToken(), m.McpAddress(), m.McpSessions(),
		m.AiReviewStrictness(), m.AiReviewWith(), m.AiReviewKind(), m.AiReviewModelName(), m.BackgroundFit()} {
		conditional[l] = true
	}

	for _, g := range prefGroups(m) {
		for _, it := range g.items {
			drawn = nil
			a.openSettings(it.page)
			tt.Frame()
			seen := map[string]bool{}
			for _, l := range drawn {
				seen[l] = true
				if !indexed[it.page][l] && !status[l] && !strings.HasPrefix(l, m.KnownHostsFile("")) {
					t.Errorf("%s: %q is not in the settings' index", it.page, l)
				}
			}
			for l := range indexed[it.page] {
				if !seen[l] && !conditional[l] {
					t.Errorf("%s: %q is in the index but not on the page", it.page, l)
				}
			}
		}
	}
}
