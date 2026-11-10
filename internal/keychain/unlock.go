package keychain

// Unlock explicitly accesses the system keychain and verifies the encrypted
// store, retaining the master key only after successful verification.
func (k *Keychain) Unlock() error { return k.unlock(false) }

// Silent is a system Backend that can read without asking the user.
type Silent interface {
	GetSilently(key string) ([]byte, error)
}

// silentSystem reads the existing master key without asking and never
// creates one.
type silentSystem struct{ Silent }

func (s silentSystem) Get(key string) ([]byte, error) { return s.GetSilently(key) }
func (silentSystem) Set(string, []byte) error         { return ErrInteractionRequired }
func (silentSystem) Delete(string) error              { return ErrInteractionRequired }

// TryUnlock unlocks an existing store only when the system keychain hands
// over the master key without asking, as after the user always allowed
// SSHYard. Otherwise it fails and the store stays locked for Unlock.
func (k *Keychain) TryUnlock() error { return k.unlock(true) }

func (k *Keychain) unlock(silent bool) error {
	k.unlockMu.Lock()
	defer k.unlockMu.Unlock()
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return &OpError{Op: "closed", Err: ErrLocked}
	}
	b, ok := k.b.(*encryptedStore)
	if !ok || len(b.key) != 0 {
		k.mu.Unlock()
		return nil
	}
	system, exists, protected := b.system, b.exists, b.protected
	k.mu.Unlock()
	if protected {
		if silent {
			return &OpError{Op: "master-read", Err: ErrInteractionRequired}
		}
		return &OpError{Op: "master-password", Err: ErrPasswordRequired}
	}
	if system == nil {
		return &OpError{Op: "master-read", Err: ErrUnavailable}
	}
	if silent {
		s, ok := system.(Silent)
		if !ok || !exists {
			return &OpError{Op: "master-read", Err: ErrInteractionRequired}
		}
		system = silentSystem{s}
	}
	key, err := master(system, exists)
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

// Unlocked reports whether this open store is ready for secret operations.
// Explicitly injected test backends do not require unlocking.
func (k *Keychain) Unlocked() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return false
	}
	if b, ok := k.b.(*encryptedStore); ok {
		return len(b.key) != 0
	}
	return true
}
