package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTrayPreferencesDefaults(t *testing.T) {
	assertDefaults := func(s Settings) {
		t.Helper()
		if s.TrayClick != TrayClickPanel || s.TrayMaterial != MaterialTranslucent || s.TrayHeight != DefaultTrayHeight {
			t.Fatalf("tray defaults: click=%q material=%q height=%d", s.TrayClick, s.TrayMaterial, s.TrayHeight)
		}
	}
	assertDefaults(Default())
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, data := range []string{"", `{}`, `{"trayClick":"invalid","trayMaterial":"invalid","trayHeight":-1}`} {
		if data != "" {
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		s, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		assertDefaults(s)
	}
}

func TestTrayHeightNormalization(t *testing.T) {
	for _, tc := range []struct{ input, want int }{
		{-1, DefaultTrayHeight}, {0, DefaultTrayHeight}, {1, MinTrayHeight},
		{MinTrayHeight, MinTrayHeight}, {700, 700}, {MaxTrayHeight, MaxTrayHeight}, {MaxTrayHeight + 1, MaxTrayHeight},
	} {
		t.Run(fmt.Sprint(tc.input), func(t *testing.T) {
			s := Default()
			s.TrayHeight = tc.input
			s.Normalize()
			if s.TrayHeight != tc.want {
				t.Fatalf("height %d normalized to %d, want %d", tc.input, s.TrayHeight, tc.want)
			}
		})
	}
}

func TestTrayPreferencesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, click := range []string{TrayClickPanel, TrayClickWindow} {
		for _, material := range []string{MaterialTranslucent, MaterialOpaque} {
			s := Default()
			s.TrayClick, s.TrayMaterial, s.TrayHeight = click, material, 700
			s.ChromeMaterial = MaterialOpaque
			if material == MaterialOpaque {
				s.ChromeMaterial = MaterialTranslucent
			}
			if err := Save(path, s); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.TrayClick != click || got.TrayMaterial != material || got.TrayHeight != 700 || got.ChromeMaterial != s.ChromeMaterial {
				t.Fatalf("tray preferences did not survive reload independently: click=%q material=%q height=%d chrome=%q", got.TrayClick, got.TrayMaterial, got.TrayHeight, got.ChromeMaterial)
			}
		}
	}
}
