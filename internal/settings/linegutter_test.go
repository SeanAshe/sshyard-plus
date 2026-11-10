package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalLineGutter(t *testing.T) {
	if d := Default(); d.TerminalLineNumbers || d.TerminalTimestamps {
		t.Fatal("line numbers and timestamps must default off")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(path); err != nil || got.TerminalLineNumbers || got.TerminalTimestamps {
		t.Fatalf("empty file: numbers %v, times %v, error %v", got.TerminalLineNumbers, got.TerminalTimestamps, err)
	}
	for _, tc := range []struct{ numbers, times bool }{{true, false}, {false, true}, {true, true}, {false, false}} {
		s := Default()
		s.TerminalLineNumbers, s.TerminalTimestamps = tc.numbers, tc.times
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.TerminalLineNumbers != tc.numbers || got.TerminalTimestamps != tc.times {
			t.Fatalf("saved %+v: numbers %v, times %v, error %v", tc, got.TerminalLineNumbers, got.TerminalTimestamps, err)
		}
	}
}
