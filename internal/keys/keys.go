// Package keys reads the private keys a credential can hold: OpenSSH and
// PEM keys, and PuTTY's .ppk (versions 2 and 3, encrypted or not), which
// it converts to OpenSSH. It also makes new ed25519 keys. What it returns
// is the OpenSSH key to keep in the keychain, with its public half.
package keys

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"

	"golang.org/x/crypto/ssh"
)

var (
	// ErrPassphraseNeeded is an encrypted key read without a passphrase.
	ErrPassphraseNeeded = errors.New("keys: the key is encrypted; a passphrase is needed")
	// ErrPassphraseWrong is a passphrase that does not open the key.
	ErrPassphraseWrong = errors.New("keys: wrong passphrase")
	// ErrNotAKey is data that is no private key this package reads.
	ErrNotAKey = errors.New("keys: not a private key")
	// ErrPublicKey is a public key given where a private one is wanted.
	ErrPublicKey = errors.New("keys: this is a public key; a private key is needed")
	// ErrCorrupt is a PuTTY key whose checksum does not match.
	ErrCorrupt = errors.New("keys: the key file is damaged")
	// ErrCostly is a PuTTY key whose passphrase derivation costs more
	// than usual, though within the limits: Import declines it, deriving
	// nothing, and ImportCostly opens it. The error is a *CostError.
	ErrCostly = errors.New("keys: the key's passphrase derivation costs more than usual")
	// ErrTooCostly is a PuTTY key whose passphrase derivation costs more
	// than this package ever derives; nothing was derived.
	ErrTooCostly = errors.New("keys: the key's passphrase derivation costs more than SSHYard allows")
)

// CostError is a PuTTY key whose derivation costs more than usual
// (ErrCostly): the memory it takes, in KiB, and its passes.
type CostError struct {
	Memory uint64
	Passes uint64
}

func (e *CostError) Error() string { return ErrCostly.Error() }

// Is makes a *CostError ErrCostly.
func (e *CostError) Is(target error) bool { return target == ErrCostly }

// UnsupportedError is a key of a kind this package does not read, such
// as a DSA key or a PuTTY key derived with Argon2d.
type UnsupportedError struct{ What string }

func (e *UnsupportedError) Error() string { return "keys: unsupported " + e.What }

// Key is a private key ready to keep.
type Key struct {
	// PEM is the OpenSSH (or PEM) private key, encrypted with its
	// passphrase when it has one.
	PEM []byte
	// Public is the key's public half; for an encrypted legacy PEM key
	// read without its passphrase it is nil.
	Public ssh.PublicKey
	// Encrypted reports that PEM needs a passphrase.
	Encrypted bool
	Comment   string
}

// Fingerprint is the public key's SHA256 fingerprint, "" when unknown.
func (k Key) Fingerprint() string {
	if k.Public == nil {
		return ""
	}
	return ssh.FingerprintSHA256(k.Public)
}

// Type is the key's algorithm, as ssh-ed25519.
func (k Key) Type() string {
	if k.Public == nil {
		return ""
	}
	return k.Public.Type()
}

// AuthorizedLine is the key as a line of authorized_keys, without the
// newline: "type base64 comment".
func (k Key) AuthorizedLine() string {
	if k.Public == nil {
		return ""
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k.Public)))
	if c := strings.TrimSpace(k.Comment); c != "" && !strings.ContainsAny(c, "\r\n") {
		line += " " + c
	}
	return line
}

// IsPPK reports whether data looks like a PuTTY key file.
func IsPPK(data []byte) bool {
	return bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n\ufeff"), []byte("PuTTY-User-Key-File-"))
}

// Import reads a private key, OpenSSH, PEM or PuTTY .ppk. passphrase
// opens an encrypted one. An encrypted key read without it is
// ErrPassphraseNeeded; for an OpenSSH key the Key returned with it still
// has its public half, so it can be kept and opened later. A PuTTY key is
// converted to OpenSSH, encrypted with the same passphrase; one whose
// passphrase derivation costs more than usual is a *CostError
// (ErrCostly), with nothing derived, which ImportCostly opens.
func Import(data, passphrase []byte) (Key, error) {
	return importKey(data, passphrase, false)
}

// ImportCostly is Import, which also opens a PuTTY key whose passphrase
// derivation costs more than usual, once the user agreed to it; never
// one past the limits (ErrTooCostly). The derivation can take seconds and
// can not be interrupted: call it off the UI thread.
func ImportCostly(data, passphrase []byte) (Key, error) {
	return importKey(data, passphrase, true)
}

func importKey(data, passphrase []byte, costly bool) (Key, error) {
	data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte("\ufeff")))
	if len(data) == 0 {
		return Key{}, ErrNotAKey
	}
	if IsPPK(data) {
		return importPPK(data, passphrase, costly)
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey(data); err == nil {
		return Key{}, ErrPublicKey
	}
	if blk, _ := pem.Decode(data); blk == nil || !strings.HasSuffix(blk.Type, "PRIVATE KEY") {
		return Key{}, ErrNotAKey
	}
	pemData := append(bytes.Clone(data), '\n')
	raw, err := ssh.ParseRawPrivateKey(data)
	var pm *ssh.PassphraseMissingError
	switch {
	case err == nil:
		pub, err := publicOf(raw)
		if err != nil {
			return Key{}, err
		}
		return Key{PEM: pemData, Public: pub}, nil
	case errors.As(err, &pm):
		if len(passphrase) == 0 {
			return Key{PEM: pemData, Public: pm.PublicKey, Encrypted: true}, ErrPassphraseNeeded
		}
		raw, err = ssh.ParseRawPrivateKeyWithPassphrase(data, passphrase)
		if err != nil {
			return Key{}, classify(err)
		}
		pub, err := publicOf(raw)
		if err != nil {
			return Key{}, err
		}
		return Key{PEM: pemData, Public: pub, Encrypted: true}, nil
	default:
		return Key{}, classify(err)
	}
}

func classify(err error) error {
	s := err.Error()
	switch {
	case errors.Is(err, x509.IncorrectPasswordError):
		return ErrPassphraseWrong
	case strings.Contains(s, "unsupported key type"), strings.Contains(s, "unhandled key type"):
		return &UnsupportedError{"key type"}
	}
	return ErrNotAKey
}

func publicOf(raw any) (ssh.PublicKey, error) {
	signer, err := ssh.NewSignerFromKey(raw)
	if err != nil {
		return nil, &UnsupportedError{"key type"}
	}
	return signer.PublicKey(), nil
}

// Generate makes a new ed25519 key, encrypted with passphrase unless it
// is empty.
func Generate(comment string, passphrase []byte) (Key, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Key{}, err
	}
	return Encode(priv, comment, passphrase)
}

// Encode turns a crypto private key into a Key in OpenSSH form.
func Encode(priv any, comment string, passphrase []byte) (Key, error) {
	var blk *pem.Block
	var err error
	if len(passphrase) > 0 {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(priv, comment, passphrase)
	} else {
		blk, err = ssh.MarshalPrivateKey(priv, comment)
	}
	if err != nil {
		return Key{}, &UnsupportedError{"key type"}
	}
	pub, err := publicOf(priv)
	if err != nil {
		return Key{}, err
	}
	return Key{PEM: pem.EncodeToMemory(blk), Public: pub, Encrypted: len(passphrase) > 0, Comment: comment}, nil
}
