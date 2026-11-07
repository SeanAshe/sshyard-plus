package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type authorizationBackend struct {
	*keychain.Memory
	fail    atomic.Bool
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *authorizationBackend) Get(k string) ([]byte, error) {
	b.calls.Add(1)
	if b.entered != nil {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	if b.fail.Load() {
		return nil, &keychain.SystemError{Code: -25293, Err: errors.New("private raw system diagnostic")}
	}
	return b.Memory.Get(k)
}
func lockedTestApp(t *testing.T, b keychain.Backend) (*App, *ui.Tester) {
	t.Helper()
	dir := t.TempDir()
	a, err := New(Platform{DataDir: dir, Secrets: b, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	tt.SetLocale("en")
	tt.Frame()
	return a, tt
}

func TestUnlockFailureCancelRetryAndShortcuts(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, tt := lockedTestApp(t, b)
	a.withSecrets(nil)
	waitFor(t, tt, "authorization failure", func() bool { return a.unlock.modal })
	m := locales.In("en")
	if strings.Contains(strings.Join(tt.Texts(), " "), "private raw") {
		t.Fatalf("raw system diagnostic shown: %q", tt.Texts())
	}
	mustClick(t, tt, m.Cancel())
	tt.Frame()
	if a.unlock.modal || a.unlocked() {
		t.Fatal("cancel unlocked or reopened the modal")
	}
	calls := b.calls.Load()
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if b.calls.Load() != calls {
		t.Fatal("cancel retried without a user action")
	}
	if err := a.ApplySettings(a.Settings()); err != nil {
		t.Fatalf("ordinary settings while locked: %v", err)
	}
	tt.Key(appMods, ui.KeyK)
	tt.Frame()
	if !a.quicking {
		t.Fatal("quick connect unavailable while locked")
	}
	a.quicking = false
	tt.Frame()
	if err := a.kc.Set("x", []byte("x")); !errors.Is(err, keychain.ErrLocked) {
		t.Fatalf("write: %v", err)
	}
	b.fail.Store(false)
	mustClick(t, tt, m.SecretsRetry())
	waitFor(t, tt, "retry unlock", a.unlocked)
	if a.unlock.modal {
		t.Fatal("modal stayed after unlocking")
	}
	if keys := b.Memory.Keys(); len(keys) != 1 || keys[0] != keychain.MasterKey {
		t.Fatalf("system entries: %q", keys)
	}
}

func TestUnlockModalDismissal(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, tt := lockedTestApp(t, b)
	a.withSecrets(nil)
	waitFor(t, tt, "modal", func() bool { return a.unlock.modal })
	m := locales.In("en")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.unlock.modal || a.unlocked() {
		t.Fatal("Escape did not leave retryable locked page")
	}
	mustClick(t, tt, m.SecretsRetry())
	waitFor(t, tt, "second modal", func() bool { return a.unlock.modal })
	tt.ClickAt(10, 200)
	tt.Frame()
	if a.unlock.modal || a.unlocked() {
		t.Fatal("outside dismissal escaped lock")
	}
}

func TestUnlockBusyAndCloseLateResult(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory(), entered: make(chan struct{}), release: make(chan struct{})}
	a, tt := lockedTestApp(t, b)
	a.withSecrets(nil)
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("authorization not requested")
	}
	for i := 0; i < 5; i++ {
		a.retryUnlock()
		tt.Frame()
	}
	if b.calls.Load() != 1 {
		t.Fatalf("concurrent authorization calls: %d", b.calls.Load())
	}
	original := []byte("original layout")
	file := filepath.Join(a.p.DataDir, "layout.json")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	a.loadLayout() // An unreadable snapshot remains protected even while secrets are locked.
	a.Close()
	close(b.release)
	// Reopening the same encrypted store proves the late result released its lock.
	waitFor(t, tt, "late result released", func() bool {
		kc, err := keychain.Open(keychain.OpenOptions{Path: filepath.Join(a.p.DataDir, "secrets.enc"), LockPath: filepath.Join(a.p.DataDir, "secrets.lock"), Scope: "sshyard", System: b.Memory})
		if err != nil {
			return false
		}
		if err := kc.Close(); err != nil {
			t.Fatal(err)
		}
		return true
	})
	tt.Frame()
	if a.ready() || len(a.fwds) > 0 || a.mcp.srv != nil {
		t.Fatal("late result started business after close")
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != string(original) {
		t.Fatalf("locked exit overwrote layout: %q %v", got, err)
	}
}

func TestUnlockBlocksAutomaticServicesAndStartsOnce(t *testing.T) {
	dir := t.TempDir()
	s := settings.Default()
	s.MCPEnabled = true
	s.MCPPort = freePort(t)
	if err := settings.Save(filepath.Join(dir, "settings.json"), s); err != nil {
		t.Fatal(err)
	}
	st, err := assets.Open(filepath.Join(dir, "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := st.Add(assets.Host{Name: "box", Address: "localhost", User: "test", Auth: assets.AuthAsk})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveForward(assets.Forward{Name: "automatic", Kind: assets.ForwardLocal, HostID: h.ID, ListenPort: freePort(t), TargetAddr: "127.0.0.1", TargetPort: 22, AutoStart: true}); err != nil {
		t.Fatal(err)
	}
	layout := `{"tabs":[{"root":{"kind":"ai","host":"` + h.ID + `"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "layout.json"), []byte(layout), 0600); err != nil {
		t.Fatal(err)
	}
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, err := New(Platform{DataDir: dir, Secrets: b, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	tt.Frame()
	if b.calls.Load() != 0 || !a.ready() {
		t.Fatal("startup requested authorization or did not load metadata")
	}
	if len(a.conns) > 0 || a.mcp.srv != nil || len(a.fwds) > 0 {
		t.Fatal("locked startup ran business")
	}
	a.syncMCP()
	if a.mcp.srv != nil {
		t.Fatal("MCP bypassed gate")
	}
	b.fail.Store(false)
	a.retryUnlock()
	waitFor(t, tt, "unlocked", a.unlocked)
	if a.mcp.srv == nil || len(a.conns) != 1 || len(a.fwds) != 1 || len(a.ws.Tabs) != 0 {
		t.Fatalf("services not restored: %v %d", a.mcp.srv, len(a.conns))
	}
	srv := a.mcp.srv
	a.retryUnlock()
	a.Start()
	tt.Frame()
	if a.mcp.srv != srv || len(a.conns) != 1 || len(a.ws.Tabs) != 0 {
		t.Fatal("duplicate startup")
	}
}

func TestUnlockStorageResolutionRecovers(t *testing.T) {
	dir := t.TempDir()
	var fail atomic.Bool
	fail.Store(true)
	resolve := func() (StoragePaths, error) {
		if fail.Load() {
			return StoragePaths{}, errors.New("private path")
		}
		return StoragePaths{DataDir: dir, LockPath: filepath.Join(dir, "secrets.lock"), Scope: "test"}, nil
	}
	system := &authorizationBackend{Memory: keychain.NewMemory()}
	a, err := New(Platform{Secrets: system, ResolveStorage: resolve})
	if err == nil {
		t.Fatal("missing initial resolution error")
	}
	defer a.Close()
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	waitFor(t, tt, "path failure", func() bool { return a.unlock.modal })
	fail.Store(false)
	a.retryUnlock()
	waitFor(t, tt, "recovered", a.ready)
	if system.calls.Load() != 0 || a.unlocked() {
		t.Fatal("storage resolution recovery requested authorization")
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets.enc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("storage recovery created secrets: %v", err)
	}
	if a.Store() == nil || a.known == nil || a.p.DataDir != dir {
		t.Fatal("metadata was not reloaded")
	}
}

func TestUnlockErrorWhitelist(t *testing.T) {
	m := locales.In("en")
	for _, code := range []int{-128, -25293, -25308, -25291, -99999} {
		err := &keychain.OpError{Op: "master-read", Err: &keychain.SystemError{Code: code, Err: errors.New("secret /private/path raw diagnostic")}}
		text := unlockErrorText(m, err)
		if strings.Contains(text, "secret /") || !strings.Contains(text, "OSStatus") || !strings.Contains(text, m.SecretsStageReadMaster()) {
			t.Fatalf("unsafe/missing diagnostic: %q", text)
		}
	}
}

func TestStartupDoesNotAuthorizeSecrets(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	a, tt := lockedTestApp(t, b)
	tt.Frame()
	if !a.ready() || a.unlocked() || a.unlock.modal || b.calls.Load() != 0 {
		t.Fatal("startup was not ready without authorization")
	}
	if _, err := os.Stat(filepath.Join(a.p.DataDir, "secrets.enc")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup persisted secrets: %v", err)
	}
	if err := a.ApplySettings(a.Settings()); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenLocalTerminal()
	if err != nil {
		t.Fatal(err)
	}
	a.CloseSession(id)
	a.openSettings(prefAppearance)
	tt.Frame()
	if b.calls.Load() != 0 || a.unlock.modal {
		t.Fatal("local terminal or settings authorized secrets")
	}
}

func TestUnlockContinuationRetryAndCancel(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	b.fail.Store(true)
	a, tt := lockedTestApp(t, b)
	ran := 0
	a.withSecrets(func() { ran++ })
	waitFor(t, tt, "failed", func() bool { return a.unlock.modal })
	b.fail.Store(false)
	mustClick(t, tt, locales.In("en").SecretsRetry())
	waitFor(t, tt, "resumed action", func() bool { return ran == 1 })
	if !a.unlocked() {
		t.Fatal("resumed action without unlocked store")
	}
}

func TestExistingSecretsStartupAndConnectionUnlock(t *testing.T) {
	dir := t.TempDir()
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	kc, err := keychain.Open(keychain.OpenOptions{Path: filepath.Join(dir, "secrets.enc"), LockPath: filepath.Join(dir, "secrets.lock"), Scope: "sshyard", System: b.Memory})
	if err != nil {
		t.Fatal(err)
	}
	if err := kc.Set("saved", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := kc.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Platform{DataDir: dir, Secrets: b, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	tt.SetLocale("en")
	tt.Frame()
	if b.calls.Load() != 0 || a.unlocked() || !a.ready() {
		t.Fatal("existing store startup authorized")
	}
	h, err := a.store.Add(assets.Host{Name: "saved host", Address: "localhost", Port: 22, User: "test", Auth: assets.AuthPassword})
	if err != nil {
		t.Fatal(err)
	}
	b.fail.Store(true)
	id, err := a.Connect(h.ID, openAt{})
	if err != nil || id == 0 {
		t.Fatalf("connect %d: %v", id, err)
	}
	waitFor(t, tt, "connection unlock failure", func() bool { return a.unlock.modal })
	mustClick(t, tt, locales.In("en").Cancel())
	tt.Frame()
	if a.unlock.after != nil || a.unlocked() {
		t.Fatal("cancel retained connection continuation")
	}
	calls := b.calls.Load()
	for range 3 {
		tt.Frame()
	}
	if b.calls.Load() != calls {
		t.Fatal("cancelled connection retried authorization")
	}
	cr, err := a.store.SaveCredential(assets.Credential{Name: "typed credential", Kind: assets.CredPassword, User: "test"})
	if err != nil {
		t.Fatal(err)
	}
	h.Auth, h.CredentialID = assets.AuthCredential, cr.ID
	a.typed[keychain.Cred(cr.ID, keychain.Password)] = "one run"
	if a.connectionNeedsSecrets(h) {
		t.Fatal("typed credential requested unlock")
	}
	px, err := a.store.SaveProxy(assets.Proxy{Name: "authenticated proxy", Kind: assets.ProxyHTTP, Address: "localhost", Port: 8080, User: "proxy-user"})
	if err != nil {
		t.Fatal(err)
	}
	h.ProxyID = px.ID
	if !a.connectionNeedsSecrets(h) {
		t.Fatal("typed credential bypassed saved proxy unlock")
	}
}

func TestCloseAfterUnlockBeforeUIAcceptsResult(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	a, _ := lockedTestApp(t, b)
	ran := false
	a.withSecrets(func() { ran = true })
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.unlock.mu.Lock()
		completed := a.unlock.completed
		a.unlock.mu.Unlock()
		if completed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unlock worker did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	a.Close()
	a.drain()
	if ran || a.unlocked() {
		t.Fatal("closed app accepted queued unlock")
	}
	kc, err := keychain.OpenLocked(keychain.OpenOptions{Path: filepath.Join(a.p.DataDir, "secrets.enc"), LockPath: filepath.Join(a.p.DataDir, "secrets.lock"), Scope: "sshyard", System: b.Memory})
	if err != nil {
		t.Fatalf("closed app retained lifetime lock: %v", err)
	}
	if err := kc.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLockedChatCloseAndReplacementPreserveContent(t *testing.T) {
	b := &authorizationBackend{Memory: keychain.NewMemory()}
	a, _ := lockedTestApp(t, b)
	h := savedHost(t, a, "first", "first.example")
	other := savedHost(t, a, "second", "second.example")
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	c.ai.conv = "unsaved-chat"
	c.ai.items = []aiItem{{kind: "user", text: "unsaved content"}}
	a.CloseSession(id)
	if a.conns[id] != c || a.ws.TabOf(id) == nil {
		t.Fatal("failed history save discarded pane")
	}
	if got, err := a.reuse(c, other.ID, 0); got != id || !errors.Is(err, keychain.ErrLocked) {
		t.Fatalf("replace unsaved chat: %d %v", got, err)
	}
	if a.conns[id] != c || c.host.ID != h.ID || len(c.ai.items) != 1 {
		t.Fatal("failed history save replaced content")
	}
	if b.calls.Load() != 0 {
		t.Fatal("closing chat requested background authorization")
	}
	c.ai.conv = ""
	c.ai.items = nil
	a.CloseSession(id)
	if a.conns[id] != nil {
		t.Fatal("empty locked pane could not close")
	}
}

// silentBackend hands over the master key without asking only when the user
// has always allowed SSHYard; Get is the read that would ask.
type silentBackend struct {
	authorizationBackend
	allowed atomic.Bool
	silent  atomic.Int32
}

func (b *silentBackend) GetSilently(k string) ([]byte, error) {
	b.silent.Add(1)
	if !b.allowed.Load() {
		return nil, &keychain.SystemError{Code: -25293, Err: errors.New("private raw system diagnostic")}
	}
	return b.Memory.Get(k)
}

func storedSilentApp(t *testing.T, allowed bool) (*App, *ui.Tester, *silentBackend) {
	t.Helper()
	dir := t.TempDir()
	b := &silentBackend{authorizationBackend: authorizationBackend{Memory: keychain.NewMemory()}}
	b.allowed.Store(allowed)
	kc, err := keychain.Open(keychain.OpenOptions{Path: filepath.Join(dir, "secrets.enc"), LockPath: filepath.Join(dir, "secrets.lock"), Scope: "sshyard", System: b.Memory})
	if err != nil {
		t.Fatal(err)
	}
	if err := kc.Set("secret", []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := kc.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(Platform{DataDir: dir, Secrets: b, HomeDir: dir, CacheDir: filepath.Join(dir, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	a.Start()
	tt := ui.NewTester(a.View, 1200, 760)
	tt.SetLocale("en")
	tt.Frame()
	return a, tt, b
}

func TestStartupUnlocksSilentlyWhenAlwaysAllowed(t *testing.T) {
	a, tt, b := storedSilentApp(t, true)
	waitFor(t, tt, "silent unlock", func() bool {
		if a.unlock.modal {
			t.Fatal("unlock prompt shown during silent unlock")
		}
		return a.unlocked()
	})
	tt.Frame()
	if b.calls.Load() != 0 || b.silent.Load() != 1 || a.unlock.modal {
		t.Fatalf("asking reads %d, silent reads %d, modal %v", b.calls.Load(), b.silent.Load(), a.unlock.modal)
	}
	if v, err := a.kc.Get("secret"); err != nil || string(v) != "value" {
		t.Fatalf("secret: %q %v", v, err)
	}
}

func TestStartupStaysLockedQuietlyWithoutPermission(t *testing.T) {
	a, tt, b := storedSilentApp(t, false)
	waitFor(t, tt, "silent attempt", func() bool { return b.silent.Load() == 1 && !a.unlock.probing })
	tt.Frame()
	if a.unlocked() || a.unlock.modal || a.unlock.err != nil || b.calls.Load() != 0 {
		t.Fatalf("silent failure: unlocked %v modal %v err %v asking reads %d", a.unlocked(), a.unlock.modal, a.unlock.err, b.calls.Load())
	}
	if strings.Contains(strings.Join(tt.Texts(), " "), "private raw") {
		t.Fatalf("raw system diagnostic shown: %q", tt.Texts())
	}
	a.withSecrets(nil)
	waitFor(t, tt, "explicit unlock", a.unlocked)
	if b.calls.Load() != 1 {
		t.Fatalf("explicit unlock asking reads: %d", b.calls.Load())
	}
}
