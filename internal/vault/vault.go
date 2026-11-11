// Package vault seals data with a password, for the backups that take
// SSHYard's data, secrets included, out of the keychain: the data is
// encrypted under a random vault key (XChaCha20-Poly1305), and the vault
// key is wrapped under a key derived from the password (Argon2id). The
// password itself is never stored.
//
// A sealed file is JSON: a plaintext header (the format, its version, when
// it was made and the wrapped keys) and the encrypted body, whose
// authentication covers the header too, so neither can be changed alone.
package vault

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

// Format names a sealed file in its header.
const Format = "sshyard-vault"

// Version is the version of the format this package writes and reads.
const Version = 1

var (
	// ErrFormat reports data that is not a sealed file.
	ErrFormat = errors.New("vault: not an SSHYard backup")
	// ErrVersion reports a file of a newer format than this app reads.
	ErrVersion = errors.New("vault: made by a newer version of SSHYard")
	// ErrPassword reports a password that does not open the file (or a
	// file whose wrapped key was damaged; the two can not be told apart).
	ErrPassword = errors.New("vault: wrong password")
	// ErrCorrupt reports a file whose body was damaged or changed.
	ErrCorrupt = errors.New("vault: the file is damaged")
	// ErrCostly reports a file whose password derivation costs more than
	// usual, though within the limits: Open declines it, OpenWith opens it
	// once allowed. The error is a *CostError, which says how much.
	ErrCostly = errors.New("vault: the file's password derivation costs more than usual")
	// ErrTooCostly reports a file whose password derivation costs more
	// than this package ever derives: too many wraps, or passes, memory
	// or work past the limits. Nothing was derived.
	ErrTooCostly = errors.New("vault: the file's password derivation costs more than SSHYard allows")
)

// Params are the cost of Argon2id: passes, memory in KiB, and lanes.
type Params struct {
	Time    uint32 `json:"t"`
	Memory  uint32 `json:"m"`
	Threads uint8  `json:"p"`
}

// DefaultParams are what Seal uses in the app: 3 passes over 64 MiB on 4
// lanes: well under a second on a current machine, and costly to guess
// against.
var DefaultParams = Params{Time: 3, Memory: 64 << 10, Threads: 4}

// The bounds of what Open derives with, so that a crafted header can not
// make it allocate gigabytes or run for minutes. Every wrap is checked,
// and the cost of them all counted, before the first is derived.
//
// Work is passes × memory in KiB, summed over the wraps: Argon2 fills its
// memory once a pass, so its time grows with that product. SSHYard has
// only ever written one wrap with DefaultParams (3 passes over 64 MiB,
// 192 MiB of work, unchanged since backups began), and a stored Key the
// same.
const (
	// maxWraps bounds the wraps of a file; SSHYard writes one.
	maxWraps = 8
	// maxTime and maxMemory bound one wrap: 16 passes over 1 GiB.
	maxTime   = 16
	maxMemory = 1 << 20 // KiB
	saltSize  = 16
	maxSalt   = 64
	// wrappedSize is a sealed vault key: the key and its tag.
	wrappedSize = chacha20poly1305.KeySize + chacha20poly1305.Overhead
)

// The budgets of a file's derivation, variables so that tests can lower
// them. Up to the usual, Open derives without asking: five times what
// DefaultParams costs, well under a second on a current machine. Past it
// and up to the limit, OpenWith derives once the user agrees. The limit
// is one wrap at maxTime and maxMemory, so that all the wraps together
// cost no more than the costliest single one could: some seconds, and
// never more than 1 GiB at once, as wraps are derived one after another.
var (
	usualMemory uint64 = 256 << 10 // KiB, the most one wrap takes: 256 MiB
	usualWork   uint64 = 1 << 20   // KiB × passes, all wraps: 1 GiB once
	maxWork     uint64 = maxTime * maxMemory
)

// valid reports parameters Argon2id derives with, whatever they cost.
func (p Params) valid() bool {
	return p.Time >= 1 && p.Memory >= 8 && p.Threads >= 1
}

func (p Params) ok() bool {
	return p.valid() && p.Time <= maxTime && p.Memory <= maxMemory
}

// memory is what Argon2id takes with p, in KiB: at least 8 KiB a lane.
func (p Params) memory() uint64 {
	return max(uint64(p.Memory), 8*uint64(p.Threads))
}

// Cost is what opening a sealed file derives: the password wraps tried,
// their passes in all, the most memory one takes and the work of them
// all (passes × memory), in KiB.
type Cost struct {
	Wraps  int
	Passes uint64
	Memory uint64 // KiB
	Work   uint64 // KiB × passes
}

// usual reports a cost Open derives without asking.
func (c Cost) usual() bool {
	return c.Memory <= usualMemory && c.Work <= usualWork
}

// CostError is a file whose derivation costs more than usual: ErrCostly,
// with what it costs.
type CostError struct{ Cost Cost }

func (e *CostError) Error() string { return ErrCostly.Error() }

// Is makes a *CostError ErrCostly.
func (e *CostError) Is(target error) bool { return target == ErrCostly }

// wrap is the vault key sealed under a key derived from a password.
type wrap struct {
	By     string `json:"by"` // "password"
	KDF    string `json:"kdf"`
	Params Params `json:"params"`
	Salt   []byte `json:"salt"`
	Nonce  []byte `json:"nonce"`
	Key    []byte `json:"key"`
}

type header struct {
	Format  string    `json:"format"`
	Version int       `json:"version"`
	Created time.Time `json:"created"`
	Wraps   []wrap    `json:"wraps"`
	Nonce   []byte    `json:"nonce"`
}

type file struct {
	Header json.RawMessage `json:"header"`
	Body   []byte          `json:"body"`
}

// wrapAAD binds a wrapped key to its use.
var wrapAAD = []byte(Format + " key")

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported systems
	}
	return b
}

// derive is Argon2id; a variable so that tests can count what is derived.
var derive = func(password []byte, salt []byte, p Params) []byte {
	return argon2.IDKey(password, salt, p.Time, p.Memory, p.Threads, chacha20poly1305.KeySize)
}

// Seal encrypts data under password, deriving with p.
func Seal(data, password []byte, p Params) ([]byte, error) {
	k, err := Derive(password, p)
	if err != nil {
		return nil, err
	}
	defer k.Clear()
	return k.Seal(data)
}

// Key is what a password derives to, with a salt of its own, and how: kept
// in the local encrypted store, it seals files for automatic backups
// without the password, and those files still open with the password.
type Key struct {
	Params Params `json:"params"`
	Salt   []byte `json:"salt"`
	KEK    []byte `json:"kek"`
}

// Derive derives a Key from password with p and a new salt.
func Derive(password []byte, p Params) (Key, error) {
	if len(password) == 0 {
		return Key{}, errors.New("vault: empty password")
	}
	if !p.ok() {
		return Key{}, fmt.Errorf("vault: bad parameters %+v", p)
	}
	salt := random(saltSize)
	return Key{Params: p, Salt: salt, KEK: derive(password, salt, p)}, nil
}

// Valid reports whether k can seal: a key of the right size and a salt
// and parameters Open will take.
func (k Key) Valid() bool {
	return len(k.KEK) == chacha20poly1305.KeySize && len(k.Salt) >= saltSize && k.Params.ok()
}

// Clear zeroes the derived key.
func (k Key) Clear() { clear(k.KEK) }

// Seal encrypts data as Seal does with the password k came from.
func (k Key) Seal(data []byte) ([]byte, error) {
	if !k.Valid() {
		return nil, errors.New("vault: invalid key")
	}
	key := random(chacha20poly1305.KeySize)
	w := wrap{By: "password", KDF: "argon2id", Params: k.Params, Salt: k.Salt, Nonce: random(chacha20poly1305.NonceSizeX)}
	kek, _ := chacha20poly1305.NewX(k.KEK)
	w.Key = kek.Seal(nil, w.Nonce, key, wrapAAD)
	h := header{Format: Format, Version: Version, Created: time.Now().UTC().Truncate(time.Second), Wraps: []wrap{w}, Nonce: random(chacha20poly1305.NonceSizeX)}
	hb, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	aead, _ := chacha20poly1305.NewX(key)
	return json.Marshal(file{Header: hb, Body: aead.Seal(nil, h.Nonce, data, hb)})
}

// parse reads the header of a sealed file, and the exact bytes the body's
// authentication covers.
func parse(b []byte) (file, header, []byte, error) {
	var f file
	var h header
	if json.Unmarshal(b, &f) != nil || len(f.Header) == 0 {
		return f, h, nil, ErrFormat
	}
	// The header as it was sealed: compact, as json.Marshal wrote it.
	var aad bytes.Buffer
	if json.Compact(&aad, f.Header) != nil || json.Unmarshal(f.Header, &h) != nil || h.Format != Format {
		return f, h, nil, ErrFormat
	}
	if h.Version > Version {
		return f, h, nil, ErrVersion
	}
	if h.Version < 1 || len(h.Nonce) != chacha20poly1305.NonceSizeX {
		return f, h, nil, ErrFormat
	}
	return f, h, aad.Bytes(), nil
}

// Check reports whether b is a sealed file this package can open, without
// the password: ErrFormat, ErrVersion, ErrTooCostly or nil. It derives
// nothing.
func Check(b []byte) error {
	_, h, _, err := parse(b)
	if err == nil {
		_, _, err = plan(h.Wraps)
	}
	return err
}

// Created is when the sealed file b was made.
func Created(b []byte) (time.Time, error) {
	_, h, _, err := parse(b)
	return h.Created, err
}

// plan checks the wraps of a header before any is derived: how many there
// are, the fields and parameters of each password wrap, and what they all
// cost. It returns the wraps to derive, and their cost. A wrap of another
// kind is left alone.
func plan(ws []wrap) ([]wrap, Cost, error) {
	var c Cost
	if len(ws) > maxWraps {
		return nil, c, ErrTooCostly
	}
	var out []wrap
	for _, w := range ws {
		if w.By != "password" {
			continue
		}
		if w.KDF != "argon2id" || !w.Params.valid() || len(w.Salt) < saltSize || len(w.Salt) > maxSalt ||
			len(w.Nonce) != chacha20poly1305.NonceSizeX || len(w.Key) != wrappedSize {
			return nil, c, ErrFormat
		}
		if !w.Params.ok() {
			return nil, c, ErrTooCostly
		}
		mem := w.Params.memory()
		c.Wraps++
		c.Passes += uint64(w.Params.Time)
		c.Memory = max(c.Memory, mem)
		c.Work += uint64(w.Params.Time) * mem
		out = append(out, w)
	}
	if c.Work > maxWork {
		return nil, c, ErrTooCostly
	}
	return out, c, nil
}

// Open decrypts a file Seal made, with its password. A file whose
// derivation costs more than usual is a *CostError (ErrCostly), with
// nothing derived: OpenWith opens it.
func Open(b, password []byte) ([]byte, error) {
	return OpenWith(context.Background(), b, password, false)
}

// OpenWith is Open, which with costly also opens a file whose derivation
// costs more than usual, never past the limits (ErrTooCostly). Once ctx is
// done it stops with ctx's error before the next wrap: one derivation
// under way can not be interrupted and runs to its end, so cancelling
// only keeps the wraps after it from being derived.
func OpenWith(ctx context.Context, b, password []byte, costly bool) ([]byte, error) {
	f, h, aad, err := parse(b)
	if err != nil {
		return nil, err
	}
	wraps, cost, err := plan(h.Wraps)
	if err != nil {
		return nil, err
	}
	if !costly && !cost.usual() {
		return nil, &CostError{Cost: cost}
	}
	var key []byte
	for _, w := range wraps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		kek, _ := chacha20poly1305.NewX(derive(password, w.Salt, w.Params))
		if k, err := kek.Open(nil, w.Nonce, w.Key, wrapAAD); err == nil && len(k) == chacha20poly1305.KeySize {
			key = k
			break
		}
	}
	if key == nil {
		return nil, ErrPassword
	}
	aead, _ := chacha20poly1305.NewX(key)
	data, err := aead.Open(nil, h.Nonce, f.Body, aad)
	if err != nil {
		return nil, ErrCorrupt
	}
	return data, nil
}
