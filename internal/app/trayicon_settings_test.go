package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
)

func TestTrayIconSettings(t *testing.T) {
	a, tt := newTestApp(t)
	var styles []string
	a.p.SetTrayIcon = func(style string) error { styles = append(styles, style); return nil }
	a.openSettings(prefGeneral)
	tt.Frame()
	m := locales.In("en")
	for _, choice := range []struct{ label, style string }{{m.TrayIconMono(), settings.TrayIconMono}, {m.TrayIconColor(), settings.TrayIconColor}} {
		current := m.TrayIconMono()
		if a.Settings().TrayIcon == settings.TrayIconColor {
			current = m.TrayIconColor()
		}
		before := len(styles)
		mustClick(t, tt, current)
		tt.Frame()
		mustClick(t, tt, choice.label)
		tt.Frame()
		if a.Settings().TrayIcon != choice.style || len(styles) != before+1 || styles[before] != choice.style {
			t.Fatalf("live settings %q, callback %v", a.Settings().TrayIcon, styles)
		}
		saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
		if err != nil || saved.TrayIcon != choice.style {
			t.Fatalf("saved %q, error %v", saved.TrayIcon, err)
		}
	}
}

func TestTrayIconApplyFailure(t *testing.T) {
	a, _ := newTestApp(t)
	old := a.Settings()
	if err := a.ApplySettings(old); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("tray update rejected")
	var calls int
	a.p.SetTrayIcon = func(string) error { calls++; return failure }
	if err := a.ApplySettings(old); err != nil || calls != 0 {
		t.Fatalf("unchanged style calls=%d, error %v", calls, err)
	}
	next := old
	next.TrayIcon = settings.TrayIconMono
	if err := a.ApplySettings(next); !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("calls=%d, error %v", calls, err)
	}
	saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
	if err != nil || saved.TrayIcon != old.TrayIcon || a.Settings().TrayIcon != old.TrayIcon {
		t.Fatalf("failure changed setting: memory %q saved %q, error %v", a.Settings().TrayIcon, saved.TrayIcon, err)
	}
}

func TestTrayIconSaveRollback(t *testing.T) {
	for _, failRollback := range []bool{false, true} {
		t.Run(strconv.FormatBool(failRollback), func(t *testing.T) {
			a, _ := newTestApp(t)
			old := a.Settings()
			if err := a.ApplySettings(old); err != nil {
				t.Fatal(err)
			}
			// A non-directory data path makes persistence fail on every platform.
			blocked := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(blocked, []byte("block"), 0o600); err != nil {
				t.Fatal(err)
			}
			dataDir := a.p.DataDir
			a.p.DataDir = blocked
			defer func() { a.p.DataDir = dataDir }()
			rollbackFailure := errors.New("rollback rejected")
			var styles []string
			a.p.SetTrayIcon = func(style string) error {
				styles = append(styles, style)
				if style == old.TrayIcon && failRollback {
					return rollbackFailure
				}
				return nil
			}
			next := old
			next.TrayIcon = settings.TrayIconMono
			err := a.ApplySettings(next)
			if err == nil || errors.Is(err, rollbackFailure) != failRollback {
				t.Fatalf("save/rollback error %v", err)
			}
			if !slices.Equal(styles, []string{settings.TrayIconMono, old.TrayIcon}) || a.Settings().TrayIcon != old.TrayIcon {
				t.Fatalf("rollback calls %v, memory %q", styles, a.Settings().TrayIcon)
			}
			saved, loadErr := settings.Load(filepath.Join(dataDir, "settings.json"))
			if loadErr != nil || saved.TrayIcon != old.TrayIcon {
				t.Fatalf("failed save changed persisted style %q, error %v", saved.TrayIcon, loadErr)
			}
		})
	}
}
