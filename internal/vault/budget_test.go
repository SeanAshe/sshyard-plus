package vault

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
)

// countDerive counts what Open derives, for the test.
func countDerive(t *testing.T) *int {
	t.Helper()
	n := new(int)
	orig := derive
	derive = func(password, salt []byte, p Params) []byte {
		*n++
		return orig(password, salt, p)
	}
	t.Cleanup(func() { derive = orig })
	return n
}

// withBudget lowers the budgets for the test.
func withBudget(t *testing.T, usual, limit uint64) {
	t.Helper()
	mem, work, hard := usualMemory, usualWork, maxWork
	usualWork, maxWork = usual, limit
	t.Cleanup(func() { usualMemory, usualWork, maxWork = mem, work, hard })
}

// firstWrap is the sealed file's one wrap, in the header edit gives.
func firstWrap(h map[string]any) map[string]any {
	return h["wraps"].([]any)[0].(map[string]any)
}

// copyWrap is w with another salt: a wrap the password does not open.
func copyWrap(w map[string]any, salt byte) map[string]any {
	c := map[string]any{}
	for k, v := range w {
		c[k] = v
	}
	s := make([]byte, saltSize)
	s[0] = salt
	c["salt"] = base64.StdEncoding.EncodeToString(s)
	return c
}

// TestWrapCount: a file of more wraps than SSHYard ever writes is
// refused before any is derived.
func TestWrapCount(t *testing.T) {
	n := countDerive(t)
	b, _ := Seal([]byte("data"), []byte("pw"), cheap)
	many := edit(t, b, func(h map[string]any) {
		w := firstWrap(h)
		ws := []any{}
		for i := range maxWraps {
			ws = append(ws, copyWrap(w, byte(i+1)))
		}
		h["wraps"] = append(ws, w)
	})
	*n = 0
	if err := Check(many); !errors.Is(err, ErrTooCostly) {
		t.Fatalf("check: %v", err)
	}
	if _, err := OpenWith(context.Background(), many, []byte("pw"), true); !errors.Is(err, ErrTooCostly) {
		t.Fatalf("open: %v", err)
	}
	if *n != 0 {
		t.Fatalf("derived %d wraps", *n)
	}
}

// TestMalformedWraps: a password wrap whose fields are wrong is refused
// before anything is derived, even after a good one.
func TestMalformedWraps(t *testing.T) {
	n := countDerive(t)
	b, _ := Seal([]byte("data"), []byte("pw"), cheap)
	b64 := func(size int) string { return base64.StdEncoding.EncodeToString(make([]byte, size)) }
	for name, fn := range map[string]func(w map[string]any){
		"kdf":        func(w map[string]any) { w["kdf"] = "scrypt" },
		"short salt": func(w map[string]any) { w["salt"] = b64(saltSize - 1) },
		"long salt":  func(w map[string]any) { w["salt"] = b64(maxSalt + 1) },
		"nonce":      func(w map[string]any) { w["nonce"] = b64(12) },
		"short key":  func(w map[string]any) { w["key"] = b64(wrappedSize - 1) },
		"long key":   func(w map[string]any) { w["key"] = b64(1 << 16) },
		"no passes":  func(w map[string]any) { w["params"] = map[string]any{"t": 0, "m": 64, "p": 1} },
		"no memory":  func(w map[string]any) { w["params"] = map[string]any{"t": 1, "m": 0, "p": 1} },
		"no lanes":   func(w map[string]any) { w["params"] = map[string]any{"t": 1, "m": 64, "p": 0} },
	} {
		bad := edit(t, b, func(h map[string]any) {
			good := firstWrap(h)
			w := copyWrap(good, 1)
			fn(w)
			h["wraps"] = []any{good, w}
		})
		*n = 0
		if _, err := OpenWith(context.Background(), bad, []byte("pw"), true); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: %v", name, err)
		}
		if err := Check(bad); !errors.Is(err, ErrFormat) {
			t.Errorf("%s: check %v", name, err)
		}
		if *n != 0 {
			t.Errorf("%s: derived %d wraps", name, *n)
		}
	}
	// A wrap of another kind is no password's, and left alone: the
	// password's still opens the key (the body, whose header changed, no
	// longer authenticates).
	other := edit(t, b, func(h map[string]any) {
		h["wraps"] = []any{map[string]any{"by": "recovery"}, firstWrap(h)}
	})
	if _, err := Open(other, []byte("pw")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("other wrap: %v", err)
	}
}

// TestCostlyNeedsConsent: past the usual budget, Open derives nothing and
// says what it would cost; allowed, it opens.
func TestCostlyNeedsConsent(t *testing.T) {
	n := countDerive(t)
	p := Params{Time: 2, Memory: 64, Threads: 1}
	withBudget(t, 2*64-1, 1<<20)
	b, err := Seal([]byte("data"), []byte("pw"), p)
	if err != nil {
		t.Fatal(err)
	}
	*n = 0
	_, err = Open(b, []byte("pw"))
	var ce *CostError
	if !errors.Is(err, ErrCostly) || !errors.As(err, &ce) {
		t.Fatalf("costly: %v", err)
	}
	if ce.Cost != (Cost{Wraps: 1, Passes: 2, Memory: 64, Work: 128}) {
		t.Fatalf("cost %+v", ce.Cost)
	}
	if *n != 0 {
		t.Fatalf("derived %d wraps before consent", *n)
	}
	if got, err := OpenWith(context.Background(), b, []byte("pw"), true); err != nil || string(got) != "data" {
		t.Fatalf("allowed: %q, %v", got, err)
	}
	// The memory of one wrap counts alone too.
	usualWork, usualMemory = 1<<20, 63
	if _, err := Open(b, []byte("pw")); !errors.Is(err, ErrCostly) {
		t.Fatalf("memory: %v", err)
	}
	// Within the budget, it opens as before.
	usualMemory = 64
	if got, err := Open(b, []byte("pw")); err != nil || string(got) != "data" {
		t.Fatalf("usual: %q, %v", got, err)
	}
}

// TestTooCostly: the work of all the wraps together is bounded, even
// when each is within the bounds of one, and allowing costly files does
// not lift it.
func TestTooCostly(t *testing.T) {
	n := countDerive(t)
	withBudget(t, 64, 3*64)
	b, _ := Seal([]byte("data"), []byte("pw"), cheap)
	four := edit(t, b, func(h map[string]any) {
		w := firstWrap(h)
		h["wraps"] = []any{copyWrap(w, 1), copyWrap(w, 2), copyWrap(w, 3), w}
	})
	*n = 0
	if _, err := OpenWith(context.Background(), four, []byte("pw"), true); !errors.Is(err, ErrTooCostly) {
		t.Fatalf("four wraps: %v", err)
	}
	slow := edit(t, b, func(h map[string]any) {
		firstWrap(h)["params"] = map[string]any{"t": maxTime + 1, "m": 8, "p": 1}
	})
	if _, err := OpenWith(context.Background(), slow, []byte("pw"), true); !errors.Is(err, ErrTooCostly) {
		t.Fatalf("passes: %v", err)
	}
	if *n != 0 {
		t.Fatalf("derived %d wraps", *n)
	}
}

// TestCancelStopsNextWrap: once cancelled, no further wrap is derived.
func TestCancelStopsNextWrap(t *testing.T) {
	b, _ := Seal([]byte("data"), []byte("pw"), cheap)
	three := edit(t, b, func(h map[string]any) {
		w := firstWrap(h)
		h["wraps"] = []any{copyWrap(w, 1), copyWrap(w, 2), w}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	orig := derive
	derive = func(password, salt []byte, p Params) []byte {
		n++
		cancel() // as the user does while the first is derived
		return orig(password, salt, p)
	}
	t.Cleanup(func() { derive = orig })
	if _, err := OpenWith(ctx, three, []byte("pw"), false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if n != 1 {
		t.Fatalf("derived %d wraps after cancelling", n)
	}
}
