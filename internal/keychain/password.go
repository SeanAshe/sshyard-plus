package keychain

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"

	"github.com/Licoy/sshyard/internal/vault"
	"golang.org/x/crypto/chacha20poly1305"
)

// A local master password is the fallback for a system keychain SSHYard
// cannot use, as on a Linux session without a Secret Service: the master
// key is sealed under it (vault, Argon2id) in master.enc beside
// secrets.enc, and the system keychain holds nothing. The password itself
// is never stored, and only an explicit unlock with it opens the store.
const masterFile = "master.enc"

// store is the encrypted store; the caller holds k.mu.
func (k *Keychain) store() (*encryptedStore, error) {
	if k.closed {
		return nil, &OpError{Op: "closed", Err: ErrLocked}
	}
	b, ok := k.b.(*encryptedStore)
	if !ok {
		return nil, &OpError{Op: "master-password", Err: ErrNoPassword}
	}
	return b, nil
}

// PasswordProtected reports whether a local master password, not the
// system keychain, protects the master key.
func (k *Keychain) PasswordProtected() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	b, err := k.store()
	return err == nil && b.protected
}

// CanUsePassword reports whether SetupPassword may protect the store with
// a local master password: nothing is stored yet, so no secrets are left
// behind under a master key of the system keychain.
func (k *Keychain) CanUsePassword() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	b, err := k.store()
	return err == nil && !b.protected && !b.exists && len(b.key) == 0
}

// SetupPassword creates the master key of an empty, locked store under a
// local master password, and unlocks the store with it.
func (k *Keychain) SetupPassword(password []byte) error {
	k.unlockMu.Lock()
	defer k.unlockMu.Unlock()
	k.mu.Lock()
	b, err := k.store()
	if err == nil && (b.protected || b.exists || len(b.key) != 0) {
		err = &OpError{Op: "master-password-write", Err: ErrPasswordSetup}
	}
	if err != nil {
		k.mu.Unlock()
		return err
	}
	path, kdf := b.masterPath, b.kdf
	k.mu.Unlock()
	key := make([]byte, chacha20poly1305.KeySize)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		return &OpError{Op: "master-password-write", Err: err}
	}
	if err := sealMaster(path, key, password, kdf); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return errors.Join(&OpError{Op: "closed", Err: ErrLocked}, removeMaster(path))
	}
	if err := b.unlock(key); err != nil {
		return errors.Join(err, removeMaster(path))
	}
	b.protected = true
	return nil
}

// UnlockPassword opens the master key with the local master password and
// unlocks the store with it.
func (k *Keychain) UnlockPassword(password []byte) error {
	k.unlockMu.Lock()
	defer k.unlockMu.Unlock()
	k.mu.Lock()
	b, err := k.store()
	if err == nil && !b.protected {
		err = &OpError{Op: "master-password", Err: ErrNoPassword}
	}
	if err != nil || len(b.key) != 0 {
		k.mu.Unlock()
		return err
	}
	path := b.masterPath
	k.mu.Unlock()
	key, err := openMaster(path, password)
	if err != nil {
		return err
	}
	defer clear(key)
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return &OpError{Op: "closed", Err: ErrLocked}
	}
	return b.unlock(key)
}

// unlockedMaster is the master key of an unlocked store a local master
// password protects, once current proves to be that password.
func (k *Keychain) unlockedMaster(current []byte) (*encryptedStore, []byte, error) {
	k.mu.Lock()
	b, err := k.store()
	switch {
	case err != nil:
	case !b.protected:
		err = &OpError{Op: "master-password", Err: ErrNoPassword}
	case len(b.key) == 0:
		err = &OpError{Op: "master-password", Err: ErrLocked}
	}
	if err != nil {
		k.mu.Unlock()
		return nil, nil, err
	}
	key, path := append([]byte(nil), b.key...), b.masterPath
	k.mu.Unlock()
	opened, err := openMaster(path, current)
	same := err == nil && bytes.Equal(opened, key)
	clear(opened)
	if err == nil && !same {
		err = &OpError{Op: "master-password", Err: ErrMasterInvalid}
	}
	if err != nil {
		clear(key)
		return nil, nil, err
	}
	return b, key, nil
}

// ChangePassword seals the master key under next in place of current.
func (k *Keychain) ChangePassword(current, next []byte) error {
	k.unlockMu.Lock()
	defer k.unlockMu.Unlock()
	b, key, err := k.unlockedMaster(current)
	if err != nil {
		return err
	}
	defer clear(key)
	return sealMaster(b.masterPath, key, next, b.kdf)
}

// UseSystem moves the master key from the local master password, current,
// to the system keychain, then forgets master.enc; failing, the password
// still protects it.
func (k *Keychain) UseSystem(current []byte) error {
	k.unlockMu.Lock()
	defer k.unlockMu.Unlock()
	b, key, err := k.unlockedMaster(current)
	if err != nil {
		return err
	}
	defer clear(key)
	if b.system == nil {
		return &OpError{Op: "master-write", Err: ErrUnavailable}
	}
	if err := b.system.Set(MasterKey, key); err != nil {
		return &OpError{Op: "master-write", Err: err}
	}
	check, err := b.system.Get(MasterKey)
	same := err == nil && bytes.Equal(check, key)
	clear(check)
	if err != nil {
		return &OpError{Op: "master-verify", Err: err}
	}
	if !same {
		return &OpError{Op: "master-verify", Err: ErrMasterInvalid}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := removeMaster(b.masterPath); err != nil {
		return &OpError{Op: "master-password-write", Err: err}
	}
	b.protected = false
	return nil
}

func sealMaster(path string, key, password []byte, kdf vault.Params) error {
	sealed, err := vault.Seal(key, password, kdf)
	if err != nil {
		return &OpError{Op: "master-password-write", Err: err}
	}
	if err := writeEncrypted(path, sealed); err != nil {
		return &OpError{Op: "master-password-write", Err: err}
	}
	return nil
}

func openMaster(path string, password []byte) ([]byte, error) {
	sealed, err := readEncrypted(path)
	if err != nil {
		return nil, &OpError{Op: "master-password", Err: err}
	}
	key, err := vault.Open(sealed, password)
	switch {
	case errors.Is(err, vault.ErrPassword):
		return nil, &OpError{Op: "master-password", Err: ErrWrongPassword}
	case err != nil || len(key) != chacha20poly1305.KeySize:
		clear(key)
		return nil, &OpError{Op: "master-password", Err: ErrMasterInvalid}
	}
	return key, nil
}

func removeMaster(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
