package keys

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

// testArgon is the Argon2 memory (KiB) and passes writePPK writes.
var testArgon = struct{ mem, passes string }{"8192", "8"}

// cheapPPK writes an encrypted v3 key of small Argon2 parameters.
func cheapPPK(t *testing.T, mem, passes string) (ed25519.PrivateKey, []byte) {
	t.Helper()
	old := testArgon
	testArgon.mem, testArgon.passes = mem, passes
	defer func() { testArgon = old }()
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	return ed, writePPK(t, ed, 3, "pw", "")
}

// countArgon counts what is derived, for the test.
func countArgon(t *testing.T) *int {
	t.Helper()
	n := new(int)
	orig := argonDerive
	argonDerive = func(p argonParams, passphrase []byte) []byte {
		*n++
		return orig(p, passphrase)
	}
	t.Cleanup(func() { argonDerive = orig })
	return n
}

// ppkBudget sets the usual budgets for the test (memory and work in KiB),
// and the limit of the work.
func ppkBudget(t *testing.T, usualMemory, usualPasses, usualWork, maxWork uint64) {
	t.Helper()
	um, up, uw, mw := ppkUsualMemory, ppkUsualPasses, ppkUsualWork, ppkMaxWork
	ppkUsualMemory, ppkUsualPasses, ppkUsualWork, ppkMaxWork = usualMemory, usualPasses, usualWork, maxWork
	t.Cleanup(func() { ppkUsualMemory, ppkUsualPasses, ppkUsualWork, ppkMaxWork = um, up, uw, mw })
}

// TestPPKCostly: past the usual cost, Import derives nothing and says
// what it would cost; ImportCostly, as the user agreed, opens the key.
func TestPPKCostly(t *testing.T) {
	ed, data := cheapPPK(t, "64", "4")
	n := countArgon(t)
	for name, b := range map[string][4]uint64{
		"work":   {1 << 20, 128, 4*64 - 1, 1 << 20},
		"passes": {1 << 20, 3, 1 << 20, 1 << 20},
		"memory": {63, 128, 1 << 20, 1 << 20},
	} {
		ppkBudget(t, b[0], b[1], b[2], b[3])
		*n = 0
		_, err := Import(data, []byte("pw"))
		var ce *CostError
		if !errors.Is(err, ErrCostly) || !errors.As(err, &ce) || ce.Memory != 64 || ce.Passes != 4 {
			t.Fatalf("%s: %v %+v", name, err, ce)
		}
		if *n != 0 {
			t.Fatalf("%s: derived before the user agreed", name)
		}
		if _, err := ImportCostly(data, []byte("wrong")); !errors.Is(err, ErrPassphraseWrong) {
			t.Fatalf("%s: wrong passphrase: %v", name, err)
		}
	}
	// Converting is slow (bcrypt), so once: the last budget still asks.
	k, err := ImportCostly(data, []byte("pw"))
	if err != nil {
		t.Fatalf("allowed: %v", err)
	}
	opens(t, k, ed, "pw")
	// Within the budget, as before.
	ppkBudget(t, 64, 4, 4*64, 4*64)
	if _, err := Import(data, []byte("pw")); err != nil {
		t.Fatal(err)
	}
}

// TestPPKTooCostly: past the limits, nothing is derived, allowed or not.
func TestPPKTooCostly(t *testing.T) {
	_, data := cheapPPK(t, "64", "4")
	n := countArgon(t)
	with := func(field, value string) []byte {
		i := bytes.Index(data, []byte(field+": "))
		j := i + bytes.IndexByte(data[i:], '\r')
		return append(append(bytes.Clone(data[:i]), field+": "+value...), data[j:]...)
	}
	for name, c := range map[string]struct {
		data    []byte
		maxWork uint64
	}{
		"memory": {with("Argon2-Memory", "2097152"), 1 << 40}, // 2 GiB
		"passes": {with("Argon2-Passes", "4097"), 1 << 40},
		"work":   {with("Argon2-Passes", "40"), 64 * 39},
	} {
		ppkBudget(t, 1, 1, 1, c.maxWork)
		b := c.data
		*n = 0
		for _, imp := range []func([]byte, []byte) (Key, error){Import, ImportCostly} {
			if _, err := imp(b, []byte("pw")); !errors.Is(err, ErrTooCostly) {
				t.Errorf("%s: %v", name, err)
			}
		}
		if *n != 0 {
			t.Errorf("%s: derived %d times", name, *n)
		}
	}
}

// TestPPKFieldsBeforeDerive: a key whose salt, Argon2 parameters, MAC or
// private blob are malformed is refused before anything is derived.
func TestPPKFieldsBeforeDerive(t *testing.T) {
	_, data := cheapPPK(t, "64", "4")
	n := countArgon(t)
	replace := func(old, new string) []byte {
		if !bytes.Contains(data, []byte(old)) {
			t.Fatalf("no %q", old)
		}
		return bytes.Replace(data, []byte(old), []byte(new), 1)
	}
	mac := bytes.Index(data, []byte("Private-MAC: ")) + len("Private-MAC: ")
	for name, b := range map[string][]byte{
		"short salt": replace("Argon2-Salt: 0123456789abcdef0123456789abcdef", "Argon2-Salt: 01234567"),
		"long salt":  replace("Argon2-Salt: 0123456789abcdef0123456789abcdef", "Argon2-Salt: "+string(bytes.Repeat([]byte("ab"), 65))),
		"no memory":  replace("Argon2-Memory: 64", "Argon2-Memory: 0"),
		"no passes":  replace("Argon2-Passes: 4", "Argon2-Passes: 0"),
		"short mac":  append(bytes.Clone(data[:mac]), "0011\r\n"...),
	} {
		*n = 0
		if _, err := ImportCostly(b, []byte("pw")); err == nil {
			t.Errorf("%s: imported", name)
		}
		if *n != 0 {
			t.Errorf("%s: derived %d times", name, *n)
		}
	}
}
