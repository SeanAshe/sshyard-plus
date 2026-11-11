package session

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// MaxOfferedKeys bounds the keys one login offers, the agent's and this
// computer's together: sshd's MaxAuthTries is 6 by default, and every
// key refused counts.
const MaxOfferedKeys = 5

// LocalKey is a private key file of this computer, offered by a host
// that asks for its password only when no key logs in.
type LocalKey struct {
	Path string
	// Public is the key's public half, known without its passphrase.
	Public ssh.PublicKey
	// PEM is the file; nil when only the agent holds the private half
	// (an IdentityFile naming a public key).
	PEM []byte
	// Encrypted keys need Passphrase to sign.
	Encrypted  bool
	Passphrase []byte
	// Explicit keys are named for the host by ~/.ssh/config.
	Explicit bool
}

// KeyID identifies a public key: the hex SHA-256 of its wire form, the
// digest its SHA256 fingerprint shows.
func KeyID(pub ssh.PublicKey) string {
	sum := sha256.Sum256(pub.Marshal())
	return hex.EncodeToString(sum[:])
}

// LocalKeyError is a key file of this computer the server accepts that
// cannot sign: it has no passphrase, or Wrong, the one given does not
// open it. The login stops there, so that the passphrase can be asked.
type LocalKeyError struct {
	Path   string
	ID     string
	Public ssh.PublicKey
	Wrong  bool
}

func (e *LocalKeyError) Error() string {
	if e.Wrong {
		return "session: wrong passphrase for " + e.Path
	}
	return "session: " + e.Path + " needs its passphrase"
}

// KeysRefusedError is a login that offered Offered keys and no password,
// all of them refused.
type KeysRefusedError struct {
	Offered int
	Err     error
}

func (e *KeysRefusedError) Error() string { return e.Err.Error() }
func (e *KeysRefusedError) Unwrap() error { return e.Err }

// keyOffer is the keys one login offers, the one that signed last, and
// the LocalKeyError that stopped the login.
type keyOffer struct {
	signers []ssh.Signer
	mu      sync.Mutex
	signed  string
	stop    error
}

// stopped is the LocalKeyError a key file stopped the login with, nil
// for none.
func (o *keyOffer) stopped() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.stop
}

func (o *keyOffer) sign(id string) {
	o.mu.Lock()
	o.signed = id
	o.mu.Unlock()
}

func (o *keyOffer) last() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.signed
}

// offerKeys orders the agent's keys and cr.LocalKeys for one login: the
// one that logged in last, then those that sign without asking (named by
// the config, the agent's, the default files), then the encrypted ones
// with no passphrase yet, each once and at most MaxOfferedKeys.
func offerKeys(cr Credentials, agent []ssh.Signer) *keyOffer {
	type cand struct {
		id     string
		signer ssh.Signer
		rank   int
	}
	explicit := map[string]bool{}
	for _, k := range cr.LocalKeys {
		if k.Explicit && k.Public != nil {
			explicit[KeyID(k.Public)] = true
		}
	}
	o := &keyOffer{}
	seen := map[string]bool{}
	for _, id := range cr.Skip {
		seen[id] = true
	}
	var cs []cand
	add := func(id string, s ssh.Signer, ready, named, fromAgent bool) {
		if seen[id] || cr.IdentitiesOnly && len(explicit) > 0 && !named || cr.IdentitiesOnly && len(explicit) == 0 && fromAgent {
			return
		}
		seen[id] = true
		rank := 2
		switch {
		case named:
			rank = 0
		case fromAgent:
			rank = 1
		}
		if !ready {
			rank += 3
		}
		if id == cr.Prefer {
			rank = -1
		}
		cs = append(cs, cand{id, s, rank})
	}
	for _, s := range agent {
		id := KeyID(s.PublicKey())
		add(id, recordSigner(s, id, o), true, explicit[id], true)
	}
	for _, k := range cr.LocalKeys {
		if k.Public == nil || k.PEM == nil {
			continue
		}
		id := KeyID(k.Public)
		add(id, &fileSigner{k: k, id: id, o: o}, !k.Encrypted || len(k.Passphrase) > 0, k.Explicit, false)
	}
	slices.SortStableFunc(cs, func(a, b cand) int { return a.rank - b.rank })
	for _, c := range cs[:min(len(cs), MaxOfferedKeys)] {
		o.signers = append(o.signers, c.signer)
	}
	return o
}

// fileSigner signs with a key file, opened only when the server accepts
// its public half: an encrypted key without its passphrase then stops the
// login with a LocalKeyError instead of being asked for up front.
type fileSigner struct {
	k  LocalKey
	id string
	o  *keyOffer
}

func (f *fileSigner) PublicKey() ssh.PublicKey { return f.k.Public }

func (f *fileSigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return f.SignWithAlgorithm(rand, data, "")
}

func (f *fileSigner) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	s, err := f.open()
	if err != nil {
		if errors.As(err, new(*LocalKeyError)) {
			f.o.mu.Lock()
			f.o.stop = err
			f.o.mu.Unlock()
		}
		return nil, err
	}
	sig, err := s.SignWithAlgorithm(rand, data, algorithm)
	if err == nil {
		f.o.sign(f.id)
	}
	return sig, err
}

func (f *fileSigner) open() (ssh.AlgorithmSigner, error) {
	var signer ssh.Signer
	var err error
	if f.k.Encrypted {
		if len(f.k.Passphrase) == 0 {
			return nil, &LocalKeyError{Path: f.k.Path, ID: f.id, Public: f.k.Public}
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(f.k.PEM, f.k.Passphrase)
		if errors.Is(err, x509.IncorrectPasswordError) || err != nil && strings.Contains(err.Error(), "decryption password incorrect") {
			return nil, &LocalKeyError{Path: f.k.Path, ID: f.id, Public: f.k.Public, Wrong: true}
		}
	} else {
		signer, err = ssh.ParsePrivateKey(f.k.PEM)
	}
	if err != nil {
		return nil, fmt.Errorf("session: %s: %w", f.k.Path, err)
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), f.k.Public.Marshal()) {
		return nil, fmt.Errorf("session: %s does not match its public key", f.k.Path)
	}
	as, ok := signer.(ssh.AlgorithmSigner)
	if !ok {
		return nil, fmt.Errorf("session: %s cannot choose its signature algorithm", f.k.Path)
	}
	return as, nil
}

// recordSigner notes in o that the agent's signer s signed.
func recordSigner(s ssh.Signer, id string, o *keyOffer) ssh.Signer {
	if as, ok := s.(ssh.AlgorithmSigner); ok {
		return &recordingAlgSigner{as, id, o}
	}
	return &recordingSigner{s, id, o}
}

type recordingSigner struct {
	ssh.Signer
	id string
	o  *keyOffer
}

func (r *recordingSigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	sig, err := r.Signer.Sign(rand, data)
	if err == nil {
		r.o.sign(r.id)
	}
	return sig, err
}

type recordingAlgSigner struct {
	ssh.AlgorithmSigner
	id string
	o  *keyOffer
}

func (r *recordingAlgSigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return r.SignWithAlgorithm(rand, data, "")
}

func (r *recordingAlgSigner) SignWithAlgorithm(rand io.Reader, data []byte, algorithm string) (*ssh.Signature, error) {
	sig, err := r.AlgorithmSigner.SignWithAlgorithm(rand, data, algorithm)
	if err == nil {
		r.o.sign(r.id)
	}
	return sig, err
}
