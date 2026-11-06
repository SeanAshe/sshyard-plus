package app

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// TestSettingsNav: the settings' sidebar lists every page, in both
// languages, and choosing one shows its title and description.
func TestSettingsNav(t *testing.T) {
	a, tt := newTestApp(t)
	mustClick(t, tt, "Settings")
	tt.Frame()
	en, zh := locales.In("en"), locales.In("zh-Hans")
	tok := theme.LightTokens()
	enGroups, zhGroups := prefGroups(en, tok), prefGroups(zh, tok)
	for gi, g := range enGroups {
		if !hasExact(tt, g.title) {
			t.Errorf("no group %q", g.title)
		}
		for ii, it := range g.items {
			z := zhGroups[gi].items[ii]
			if z.title == "" || z.desc == it.desc || z.desc == "" {
				t.Errorf("%s is not translated: %q %q", it.page, z.title, z.desc)
			}
			mustClick(t, tt, it.title)
			tt.Frame()
			if a.prefNow() != it.page || !tt.HasText(it.desc) {
				t.Fatalf("page %q after %q: %q", a.prefNow(), it.title, tt.Texts())
			}
		}
	}
	// The rail and the panels are the workspace's only.
	if tt.HasText(en.SystemMonitor()) {
		t.Error("the rail shows over the settings")
	}
	mustClick(t, tt, "Back")
	if a.page != pageWorkspace || !tt.HasText(en.SystemMonitor()) {
		t.Fatalf("back: page %v %q", a.page, tt.Texts())
	}
	// The settings open again on the page last shown.
	mustClick(t, tt, "Settings")
	if a.prefNow() != prefAbout {
		t.Fatalf("reopened on %q", a.prefNow())
	}
}

func TestKeyCaps(t *testing.T) {
	for _, tc := range []struct {
		goos string
		mods ui.Modifiers
		key  ui.Key
		want []string
	}{
		{"darwin", ui.Super, ui.KeyK, []string{"⌘", "K"}},
		{"darwin", ui.Super | ui.Shift, ui.KeyA, []string{"⇧", "⌘", "A"}},
		{"linux", ui.Ctrl | ui.Shift, ui.KeyT, []string{"Ctrl", "Shift", "T"}},
		{"windows", ui.Ctrl, ui.KeyPeriod, []string{"Ctrl", "."}},
		{"darwin", ui.Super | ui.Alt, ui.Key7, []string{"⌥", "⌘", "7"}},
		{"linux", ui.Ctrl | ui.Shift, ui.KeyF11, []string{"Ctrl", "Shift", "F11"}},
		{"darwin", ui.Super, ui.KeyBracketLeft, []string{"⌘", "["}},
		{"windows", ui.Ctrl | ui.Shift, ui.KeySlash, []string{"Ctrl", "Shift", "/"}},
	} {
		if got := keyCaps(tc.goos, tc.mods, tc.key); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s %v %v: %q, want %q", tc.goos, tc.mods, tc.key, got, tc.want)
		}
	}
}

func TestShortcutsPage(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 1400)
	mustClick(t, tt, "Keyboard Shortcuts")
	tt.Frame()
	if a.page != pageSettings || a.prefNow() != prefShortcuts {
		t.Fatalf("page %v %q", a.page, a.prefNow())
	}
	m := locales.In("en")
	for _, ac := range actions {
		if !hasExact(tt, ac.label(m)) {
			t.Errorf("no row %q", ac.label(m))
		}
		for _, k := range keyCaps(runtime.GOOS, ac.def.Mods, ac.def.Key) {
			if !hasExact(tt, k) {
				t.Errorf("no key %q of %q", k, ac.label(m))
			}
		}
	}
}

func TestAboutPage(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 1600)
	var opened string
	a.p.OpenPath = func(p string) error { opened = p; return nil }
	a.openSettings(prefAbout)
	tt.Frame()
	for _, s := range []string{"Version dev", runtime.GOOS + " / " + runtime.GOARCH, a.p.DataDir,
		"Encrypted local store; master key in system keychain", "github.com/Licoy/sshyard", "github.com/Licoy/sshyard/issues", "Check for Updates"} {
		if !tt.HasText(s) {
			t.Errorf("about misses %q: %q", s, tt.Texts())
		}
	}
	for _, s := range []string{runtime.Version(), "Remix Icon"} {
		if tt.HasText(s) {
			t.Errorf("about shows %q", s)
		}
	}
	mustClick(t, tt, "Open Data Folder")
	if opened != a.p.DataDir {
		t.Fatalf("opened %q", opened)
	}
}

func TestAboutLicenseLink(t *testing.T) {
	for _, tc := range []struct {
		language string
		dark     bool
	}{{"en", false}, {"en", true}, {"zh-Hans", false}, {"zh-Hans", true}} {
		t.Run(fmt.Sprintf("%s/dark=%v", tc.language, tc.dark), func(t *testing.T) {
			a, tt := newTestApp(t)
			tt.SetSize(1200, 1600)
			tt.SetLocale(tc.language)
			tt.SetDark(tc.dark)
			a.openSettings(prefAbout)
			tt.Frame()
			if !tt.HasText(locales.In(tc.language).License()) {
				t.Fatalf("about misses license label: %q", tt.Texts())
			}
			mustClick(t, tt, "github.com/Licoy/sshyard/blob/main/LICENSE")
			if got := tt.OpenedURLs(); !slices.Equal(got, []string{"https://github.com/Licoy/sshyard/blob/main/LICENSE"}) {
				t.Errorf("license opened %q", got)
			}
		})
	}
}

// TestNumberInputsFit: a number's steppers stay inside its row, flush
// with the other controls' right edge.
func TestNumberInputsFit(t *testing.T) {
	for page, edge := range map[prefPage]string{prefAI: "Full access", prefTerminal: "Deny"} {
		a, tt := newTestApp(t)
		tt.SetSize(1200, 3200)
		a.openSettings(page)
		tt.Frame()
		inc, ok1 := tt.Find("Increase")
		ref, ok2 := tt.Find(edge)
		if !ok1 || !ok2 {
			t.Fatalf("%s: no stepper or %q: %q", page, edge, tt.Texts())
		}
		// ref is a segment's text, inside the control's padding; the
		// steppers overflowed by some 60 points.
		if r, want := inc.X+inc.W, ref.X+ref.W; r > want+20 || r < want {
			t.Errorf("%s: stepper ends at %v, the controls at %v", page, r, want)
		}
	}
}

func TestStatusBar(t *testing.T) {
	a, tt := newTestApp(t)
	if !tt.HasText("No active session") {
		t.Fatalf("texts %q", tt.Texts())
	}
	if err := tt.Click("Theme"); err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, "Dark")
	tt.Frame()
	if a.Settings().Theme != theme.Dark {
		t.Fatalf("theme %q", a.Settings().Theme)
	}
	th, _ := tt.Find("Theme")
	sc, ok := tt.Find("Keyboard Shortcuts")
	if !ok || sc.X < th.X+th.W+theme.Space(1)-0.5 {
		t.Errorf("the status bar's buttons touch: %v %v", th, sc)
	}
	mustClick(t, tt, "Settings")
	if a.page != pageSettings {
		t.Fatal("the title bar's settings did not open them")
	}
	if tt.HasText("No active session") {
		t.Fatal("the status bar shows in the settings")
	}
}

// TestRailAndMonitor: the rail opens the system monitor beside the panes,
// which reads the focused terminal's host; then the host's details; the
// status bar says who is focused and how many sessions run.
func TestRailAndMonitor(t *testing.T) {
	ex := &frameExec{frames: linuxFrames(true)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 1400)
	connected(t, a, tt, srv)
	waitSample(t, tt)
	if !tt.HasText("1 session") || !hasExact(tt, "box") {
		t.Fatalf("status bar: %q", tt.Texts())
	}
	// The latency shows in the metrics bar only, not the status bar too.
	if n := textCount(tt, "Latency"); n != 1 {
		t.Errorf("latency shows %d times: %q", n, tt.Texts())
	}
	// The rail is as wide as the status bar is high.
	if r := mustFind(t, tt, "System Monitor"); r.X < 1400-railWidth || r.X+r.W > 1400 {
		t.Errorf("rail button at %v, not in a rail %v wide", r, railWidth)
	}
	mustClick(t, tt, "System Monitor")
	tt.Frame()
	if !a.panelOpen(inspectMonitor) {
		t.Fatalf("panels %q", a.panels)
	}
	for _, s := range []string{"Average usage", "2 cores", "Load average", "RAM", "4.0G / 8.0G", "Swap", "eth0", "/data", "488.3M / 1.9G", "GPU 1", "Operating system", "linux", "Connected for"} {
		if !tt.HasText(s) {
			t.Errorf("monitor misses %q: %q", s, tt.Texts())
		}
	}
	if !tt.HasText("Core 1 100%") {
		t.Errorf("no per-core bars: %q", tt.Texts())
	}

	mustClick(t, tt, "Host Details")
	tt.Frame()
	// Panels stack: the monitor stays, the host's details under it.
	if len(a.panels) != 2 || a.panels[0] != inspectMonitor || a.panels[1] != inspectHost || !tt.HasText("Average usage") {
		t.Fatalf("panels %q", a.panels)
	}
	if mon, host := mustFind(t, tt, "Average usage"), mustFind(t, tt, "Jump hosts"); mon.Y >= host.Y {
		t.Errorf("monitor at %v is not above the host's details at %v", mon, host)
	}
	for _, s := range []string{"Saved credential box-cred", "Jump hosts", "Open panes", "None (direct)"} {
		if !tt.HasText(s) {
			t.Errorf("host details miss %q: %q", s, tt.Texts())
		}
	}
	if n := textCount(tt, "Close Panel"); n != 2 {
		t.Fatalf("%d close buttons", n)
	}
	mustClick(t, tt, "Close Panel")
	tt.Frame()
	if len(a.panels) != 1 || a.panels[0] != inspectHost {
		t.Fatalf("closing the monitor left %q", a.panels)
	}
	mustClick(t, tt, "Close Panel")
	if len(a.panels) != 0 {
		t.Fatal("the panel did not close")
	}

	mustClick(t, tt, "Open Files on the Right")
	if tab := a.ws.ActiveTab(); tab.Count(layout.Files) != 1 || tab.Count(layout.Terminal) != 1 {
		t.Fatalf("items %v", tab.Items())
	}
	tt.Frame()
	if !tt.HasText("2 sessions") {
		t.Fatalf("status bar: %q", tt.Texts())
	}
}

// TestMonitorMetricsOff: with the metrics off, the monitor says so and
// leads to their settings instead of reading the host.
func TestMonitorMetricsOff(t *testing.T) {
	ex := &frameExec{frames: linuxFrames(false)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	s := a.Settings()
	s.MetricsEnabled = false
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	connected(t, a, tt, srv)
	a.panels = []inspectorKind{inspectMonitor}
	tt.Frame()
	if !tt.HasText("Metrics are turned off for this host.") {
		t.Fatalf("texts %q", tt.Texts())
	}
	mustClick(t, tt, "Open Settings")
	if a.page != pageSettings || a.prefNow() != prefMonitoring {
		t.Fatalf("page %v %q", a.page, a.prefNow())
	}
	if ex.starts.Load() != 0 {
		t.Fatal("the monitor ran the metrics command while they are off")
	}
}

func TestFmtDur(t *testing.T) {
	m := locales.In("en")
	for d, want := range map[time.Duration]string{
		-time.Second:                     "0s",
		42 * time.Second:                 "42s",
		5*time.Minute + 3*time.Second:    "5m",
		2*time.Hour + 7*time.Minute:      "2h 7m",
		4*24*time.Hour + 2*time.Hour + 1: "4d 2h",
	} {
		if got := fmtDur(m, d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
	if got := fmtDur(locales.In("zh-Hans"), 2*time.Hour+7*time.Minute); got != "2 小时 7 分" {
		t.Errorf("zh: %q", got)
	}
}

// TestNoBannerText: a server that takes the connection and stays silent
// is told apart from one that cannot be reached.
func TestNoBannerText(t *testing.T) {
	err := fmt.Errorf("%w: %w", session.ErrNoBanner, context.DeadlineExceeded)
	h := assets.Host{Name: "aliyun", Address: "203.0.113.7", Port: 22}
	if got := errText(locales.In("en"), h, err); !strings.Contains(got, "aliyun accepted the connection") {
		t.Errorf("en: %q", got)
	}
	if got := errText(locales.In("zh-Hans"), h, err); !strings.Contains(got, "SSH 服务没有任何响应") {
		t.Errorf("zh: %q", got)
	}
}

// TestPanelsStack: the rail's panels open together, each once, stacked
// in the rail's order whatever the order they opened in, in a column as
// wide as the widest wants.
func TestPanelsStack(t *testing.T) {
	a, tt := newTestApp(t)
	for _, s := range []string{"Snippets", "System Monitor", "Docker", "Host Details", "Process Usage"} {
		mustClick(t, tt, s)
		tt.Frame()
	}
	a.openPanel(inspectMonitor) // once only
	if want := []inspectorKind{inspectMonitor, inspectUsage, inspectHost, inspectSnippets, inspectDocker}; !slices.Equal(a.panels, want) {
		t.Fatalf("panels %q, want %q", a.panels, want)
	}
	if n := textCount(tt, "Close Panel"); n != 5 {
		t.Errorf("%d close buttons", n)
	}
	// Each panel's title is under the one before it. (The panels come
	// before the rail, whose buttons have the same names.)
	var y float32 = -1
	for _, s := range []string{"System Monitor", "Process Usage", "Host Details", "Snippets", "Docker"} {
		r := mustFind(t, tt, s)
		if r.Y <= y || r.X > 1200-railWidth-1 {
			t.Errorf("%s at %v, not a panel under the one before", s, r)
		}
		y = r.Y
	}
	a.togglePanel(inspectMonitor)
	tt.Frame()
	a.closePanel(inspectUsage)
	a.closePanel(inspectDocker)
	if want := []inspectorKind{inspectHost, inspectSnippets}; !slices.Equal(a.panels, want) {
		t.Fatalf("panels %q, want %q", a.panels, want)
	}
	if w := max(panelWidth(inspectHost), panelWidth(inspectSnippets)); w != snippetWidth {
		t.Errorf("column width %v", w)
	}
}
