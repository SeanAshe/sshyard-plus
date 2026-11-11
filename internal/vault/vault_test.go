package vault

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// cheap keeps the tests fast; the format is the same.
var cheap = Params{Time: 1, Memory: 64, Threads: 1}

func TestSealOpen(t *testing.T) {
	data := []byte(`{"hosts":[],"secrets":{"cred/1/password":"aHVudGVyMg=="}}`)
	b, err := Seal(data, []byte("correct horse"), cheap)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("hosts")) || bytes.Contains(b, []byte("cred/1")) {
		t.Fatal("the plaintext shows in the sealed file")
	}
	if err := Check(b); err != nil {
		t.Fatal(err)
	}
	if at, err := Created(b); err != nil || time.Since(at) > time.Minute {
		t.Fatalf("created %v, %v", at, err)
	}
	got, err := Open(b, []byte("correct horse"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("open: %q, %v", got, err)
	}
	if _, err := Open(b, []byte("wrong")); !errors.Is(err, ErrPassword) {
		t.Fatalf("wrong password: %v", err)
	}
}

// TestFreshKeys: two seals of the same data share no key, salt or nonce.
func TestFreshKeys(t *testing.T) {
	a, _ := Seal([]byte("x"), []byte("pw"), cheap)
	b, _ := Seal([]byte("x"), []byte("pw"), cheap)
	if bytes.Equal(a, b) {
		t.Fatal("two seals are the same")
	}
}

// TestIndentedStillOpens: a file reformatted by an editor (indented) still
// opens, as its header is authenticated compact.
func TestIndentedStillOpens(t *testing.T) {
	b, _ := Seal([]byte("data"), []byte("pw"), cheap)
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", "  "); err != nil {
		t.Fatal(err)
	}
	if got, err := Open(out.Bytes(), []byte("pw")); err != nil || string(got) != "data" {
		t.Fatalf("%q, %v", got, err)
	}
}

// edit changes the sealed file's header with fn.
func edit(t *testing.T, b []byte, fn func(h map[string]any)) []byte {
	t.Helper()
	var f map[string]json.RawMessage
	json.Unmarshal(b, &f)
	var h map[string]any
	json.Unmarshal(f["header"], &h)
	fn(h)
	f["header"], _ = json.Marshal(h)
	out, _ := json.Marshal(f)
	return out
}

func TestTamper(t *testing.T) {
	b, _ := Seal([]byte("data"), []byte("pw"), cheap)
	// The creation time is plaintext, but changing it breaks the body.
	moved := edit(t, b, func(h map[string]any) { h["created"] = "2001-01-01T00:00:00Z" })
	if _, err := Open(moved, []byte("pw")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("changed header: %v", err)
	}
	var f map[string]any
	json.Unmarshal(b, &f)
	body := []byte(f["body"].(string))
	body[len(body)/2] ^= 1
	f["body"] = string(body)
	flipped, _ := json.Marshal(f)
	if _, err := Open(flipped, []byte("pw")); err == nil {
		t.Fatal("a changed body opened")
	}
}

func TestFormatAndVersion(t *testing.T) {
	for _, b := range []string{"", "{}", "not json", `{"header":{"format":"other","version":1}}`, `{"hosts":[]}`} {
		if err := Check([]byte(b)); !errors.Is(err, ErrFormat) {
			t.Errorf("%q: %v", b, err)
		}
	}
	b, _ := Seal([]byte("data"), []byte("pw"), cheap)
	newer := edit(t, b, func(h map[string]any) { h["version"] = Version + 1 })
	if err := Check(newer); !errors.Is(err, ErrVersion) {
		t.Fatalf("newer: %v", err)
	}
	if _, err := Open(newer, []byte("pw")); !errors.Is(err, ErrVersion) {
		t.Fatalf("open newer: %v", err)
	}
}

// TestBoundedParams: a header asking for more than the bounds is refused
// before anything is derived, rather than exhaust the machine.
func TestBoundedParams(t *testing.T) {
	b, _ := Seal([]byte("data"), []byte("pw"), cheap)
	huge := edit(t, b, func(h map[string]any) {
		w := h["wraps"].([]any)[0].(map[string]any)
		w["params"] = map[string]any{"t": 1, "m": maxMemory * 8, "p": 1}
	})
	start := time.Now()
	if _, err := Open(huge, []byte("pw")); !errors.Is(err, ErrTooCostly) {
		t.Fatalf("huge params: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("derived with the huge parameters")
	}
	if _, err := Seal([]byte("x"), []byte("pw"), Params{}); err == nil || !strings.Contains(err.Error(), "parameters") {
		t.Fatalf("zero params: %v", err)
	}
	if _, err := Seal([]byte("x"), nil, cheap); err == nil {
		t.Fatal("sealed with no password")
	}
}

// TestKeySeal: a derived key seals without the password, files the
// password opens, each with a fresh vault key.
func TestKeySeal(t *testing.T) {
	k, err := Derive([]byte("correct horse"), cheap)
	if err != nil || !k.Valid() {
		t.Fatalf("derive: %v", err)
	}
	a, err := k.Seal([]byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := k.Seal([]byte("one"))
	if bytes.Equal(a, b) {
		t.Fatal("two seals are the same")
	}
	for _, f := range [][]byte{a, b} {
		if got, err := Open(f, []byte("correct horse")); err != nil || string(got) != "one" {
			t.Fatalf("open: %q, %v", got, err)
		}
		if _, err := Open(f, []byte("wrong")); !errors.Is(err, ErrPassword) {
			t.Fatalf("wrong password: %v", err)
		}
	}
	// A key kept as JSON, in the local encrypted store, seals the same.
	kb, _ := json.Marshal(k)
	var back Key
	if json.Unmarshal(kb, &back) != nil || !back.Valid() {
		t.Fatal("key does not survive JSON")
	}
	c, err := back.Seal([]byte("two"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Open(c, []byte("correct horse")); err != nil || string(got) != "two" {
		t.Fatalf("open: %q, %v", got, err)
	}
	k.Clear()
	if !bytes.Equal(k.KEK, make([]byte, len(k.KEK))) {
		t.Fatal("clear left the key")
	}
	if _, err := (Key{}).Seal([]byte("x")); err == nil {
		t.Fatal("sealed with no key")
	}
	if _, err := Derive(nil, cheap); err == nil {
		t.Fatal("derived from no password")
	}
}
