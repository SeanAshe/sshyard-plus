package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalStripe(t *testing.T) {
	if !Default().TerminalStripe {
		t.Fatal("striped background must default on")
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	if got, err := Load(path); err != nil || !got.TerminalStripe {
		t.Fatalf("missing file: stripe = %v, error %v", got.TerminalStripe, err)
	}
	for _, tc := range []struct {
		body string
		want bool
	}{{`{}`, true}, {`{"terminalStripe":false}`, false}, {`{"terminalStripe":true}`, true}} {
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.TerminalStripe != tc.want {
			t.Fatalf("%s: stripe = %v, want %v, error %v", tc.body, got.TerminalStripe, tc.want, err)
		}
	}
	for _, on := range []bool{false, true} {
		s := Default()
		s.TerminalStripe = on
		if err := Save(path, s); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if err != nil || got.TerminalStripe != on {
			t.Fatalf("saved %v: stripe = %v, error %v", on, got.TerminalStripe, err)
		}
	}
}
