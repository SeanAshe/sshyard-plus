package app

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/layout"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// TestSettingsNav: the settings' sidebar leads to every page, Back
// returns to the workspace, and the settings reopen on the last page.
func TestSettingsNav(t *testing.T) {
	a, tt := newTestApp(t)
	mustClick(t, tt, "Settings")
	tt.Frame()
	for _, g := range prefGroups(locales.In("en"), theme.LightTokens()) {
		for _, it := range g.items {
			mustClick(t, tt, it.title)
			tt.Frame()
			if a.prefNow() != it.page {
				t.Fatalf("page %q after %q", a.prefNow(), it.title)
			}
		}
	}
	mustClick(t, tt, "Back")
	if a.page != pageWorkspace {
		t.Fatalf("back: page %v", a.page)
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
}

func TestAboutPage(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 1600)
	var opened string
	a.p.OpenPath = func(p string) error { opened = p; return nil }
	a.openSettings(prefAbout)
	tt.Frame()
	mustClick(t, tt, "Open Data Folder")
	if opened != a.p.DataDir {
		t.Fatalf("opened %q", opened)
	}
}

func TestAboutLicenseLink(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 1600)
	a.openSettings(prefAbout)
	tt.Frame()
	mustClick(t, tt, "github.com/SeanAshe/sshyard-plus/blob/main/LICENSE")
	if got := tt.OpenedURLs(); !slices.Equal(got, []string{"https://github.com/SeanAshe/sshyard-plus/blob/main/LICENSE"}) {
		t.Errorf("license opened %q", got)
	}
}

func TestStatusBar(t *testing.T) {
	a, tt := newTestApp(t)
	if err := tt.Click("Theme"); err != nil {
		t.Fatal(err)
	}
	mustClick(t, tt, "Dark")
	tt.Frame()
	if a.Settings().Theme != theme.Dark {
		t.Fatalf("theme %q", a.Settings().Theme)
	}
	mustClick(t, tt, "Settings")
	if a.page != pageSettings {
		t.Fatal("the title bar's settings did not open them")
	}
}

// TestRailAndMonitor: side by side, the rail opens the system monitor as
// a pane beside the terminal, then the host's details; the terminal keeps the focus,
// and each closes on its own.
func TestRailAndMonitor(t *testing.T) {
	ex := &frameExec{frames: linuxFrames(true)}
	srv := startMetricSrv(t, ex.run)
	a, tt := newTestApp(t)
	tt.SetSize(1400, 1400)
	a.settings.ToolPanes = settings.ToolPanesSplit
	cn := connected(t, a, tt, srv)
	mustClick(t, tt, "System Monitor")
	tt.Frame()
	mustClick(t, tt, "Host Details")
	tt.Frame()
	tab := a.ws.ActiveTab()
	if tab.Count(layout.Monitor) != 1 || tab.Count(layout.HostInfo) != 1 || a.focused() != cn {
		t.Fatalf("items %v, focused %v", tab.Items(), a.focused())
	}
	a.ToggleTool(layout.Monitor)
	if tab.Count(layout.Monitor) != 0 || tab.Count(layout.HostInfo) != 1 {
		t.Fatalf("closing the monitor left %v", tab.Items())
	}
	a.ToggleTool(layout.HostInfo)
	if tab.Count(layout.HostInfo) != 0 {
		t.Fatal("the host's details did not close")
	}

	mustClick(t, tt, "Open Files on the Right")
	if tab := a.ws.ActiveTab(); tab.Count(layout.Files) != 1 || tab.Count(layout.Terminal) != 1 {
		t.Fatalf("items %v", tab.Items())
	}
}

// TestMonitorMetricsOff: with the metrics off, the monitor leads to
// their settings instead of reading the host.
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
	a.ShowTool(layout.Monitor)
	tt.Frame()
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
}

// TestNoBannerText: a server that takes the connection and stays silent
// is told apart from one that cannot be reached.
func TestNoBannerText(t *testing.T) {
	err := fmt.Errorf("%w: %w", session.ErrNoBanner, context.DeadlineExceeded)
	h := assets.Host{Name: "aliyun", Address: "203.0.113.7", Port: 22}
	if got := errText(locales.In("en"), h, err); !strings.Contains(got, "aliyun accepted the connection") {
		t.Errorf("en: %q", got)
	}
}

// TestHostToolPanes: the rail's panels on the host are tool panes, one
// of each in the workspace: showing one already open keeps it, toggling
// it closes it.
func TestHostToolPanes(t *testing.T) {
	a, tt := newTestApp(t)
	kinds := []layout.Kind{layout.Monitor, layout.Docker, layout.HostInfo, layout.Usage, layout.Tmux, layout.Services}
	for _, s := range []string{"System Monitor", "Docker", "Host Details", "Process Usage", "tmux Sessions", "System Services"} {
		mustClick(t, tt, s)
		tt.Frame()
	}
	a.ShowTool(layout.Monitor) // once only
	tab := a.ws.ActiveTab()
	for _, k := range kinds {
		if !isTool(k) || tab.Count(k) != 1 {
			t.Fatalf("%v: items %v", k, tab.Items())
		}
	}
	a.ToggleTool(layout.Monitor)
	a.ToggleTool(layout.Usage)
	tt.Frame()
	if tab.Count(layout.Monitor) != 0 || tab.Count(layout.Usage) != 0 || tab.Count(layout.HostInfo) != 1 {
		t.Fatalf("items %v", tab.Items())
	}
}
