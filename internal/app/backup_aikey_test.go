package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Licoy/sshyard/internal/assets"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
)

// Every key here is fictional and every endpoint a local test server.
const (
	localAIKey = "sk-local-fictional"
	newAIKey   = "sk-backup-fictional"
)

// aiEndpointServer is a local model endpoint recording the Authorization
// headers it receives.
type aiEndpointServer struct {
	*httptest.Server
	mu   sync.Mutex
	auth []string
}

func newAIEndpointServer(t *testing.T) *aiEndpointServer {
	t.Helper()
	s := &aiEndpointServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.auth = append(s.auth, r.Header.Get("Authorization"))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *aiEndpointServer) received(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.auth {
		if strings.Contains(h, key) {
			return true
		}
	}
	return false
}

// withAIProvider gives a the provider prov1 at base with localAIKey.
func withAIProvider(t *testing.T, a *App, base string) {
	t.Helper()
	s := a.settings
	s.AIProviders = []settings.AIProvider{{ID: "prov1", Name: "Work", BaseURL: base, Model: "m", Proxy: settings.ProxyDirect}}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.AIKey("prov1"), []byte(localAIKey)); err != nil {
		t.Fatal(err)
	}
}

// movedProviderBackup is a backup of a's entries with prov1 at base.
func movedProviderBackup(a *App, base string, secrets map[string][]byte) backupPayload {
	s := a.settings
	s.AIProviders = []settings.AIProvider{{ID: "prov1", Name: "Work", BaseURL: base, Model: "m", Proxy: settings.ProxyDirect}}
	return backupPayload{Assets: a.store.Snapshot(), Settings: s, Secrets: secrets}
}

// askModels lists prov1's models as the app would, returning the client error.
func askModels(t *testing.T, a *App) error {
	t.Helper()
	p := a.providerByID("prov1")
	if p == nil {
		t.Fatal("provider missing after restore")
	}
	cl, err := a.providerClient(p)
	if err != nil {
		return err
	}
	_, err = cl.ListModels(context.Background())
	return err
}

func TestRestoreMovedAIProviderDropsLocalKey(t *testing.T) {
	a, _, _ := backupApp(t)
	old, moved := newAIEndpointServer(t), newAIEndpointServer(t)
	withAIProvider(t, a, old.URL+"/v1")
	payload := movedProviderBackup(a, moved.URL+"/v1", nil)
	if !a.backupNeedsSecrets(payload) {
		t.Fatal("restore moving a provider does not ask for the secret store")
	}
	if err := a.restoreBackup(en, payload); err != nil {
		t.Fatal(err)
	}
	if got := a.providerByID("prov1").BaseURL; got != moved.URL+"/v1" {
		t.Fatalf("provider endpoint %q", got)
	}
	if err := askModels(t, a); !errors.Is(err, errAIKey) {
		t.Fatalf("moved provider without a key: %v", err)
	}
	if moved.received(localAIKey) || old.received(localAIKey) {
		t.Fatal("local key was sent after the endpoint moved")
	}
	if ok, err := a.kc.Exists(keychain.AIKey("prov1")); ok || err != nil {
		t.Fatal("local key kept for a moved endpoint")
	}
}

func TestRestoreSameAIEndpointKeepsLocalKey(t *testing.T) {
	a, _, _ := backupApp(t)
	srv := newAIEndpointServer(t)
	withAIProvider(t, a, srv.URL+"/v1")
	// Another path, scheme and host case on the same service.
	same := "HTTP://" + strings.ToUpper(strings.TrimPrefix(srv.URL, "http://")) + "/v2"
	payload := movedProviderBackup(a, same, nil)
	if a.backupNeedsSecrets(payload) {
		t.Fatal("restore on the same endpoint asks for the secret store")
	}
	if err := a.restoreBackup(en, payload); err != nil {
		t.Fatal(err)
	}
	if got, err := a.kc.Get(keychain.AIKey("prov1")); err != nil || string(got) != localAIKey {
		t.Fatal("same-endpoint restore lost the local key")
	}
	if err := askModels(t, a); err != nil {
		t.Fatal(err)
	}
	if !srv.received(localAIKey) {
		t.Fatal("same endpoint did not get the local key")
	}
}

func TestRestoreMovedAIProviderUsesBackupKey(t *testing.T) {
	a, _, _ := backupApp(t)
	old, moved := newAIEndpointServer(t), newAIEndpointServer(t)
	withAIProvider(t, a, old.URL+"/v1")
	payload := movedProviderBackup(a, moved.URL+"/v1", map[string][]byte{keychain.AIKey("prov1"): []byte(newAIKey)})
	if err := a.restoreBackup(en, payload); err != nil {
		t.Fatal(err)
	}
	if err := askModels(t, a); err != nil {
		t.Fatal(err)
	}
	if !moved.received(newAIKey) || moved.received(localAIKey) || old.received(localAIKey) {
		t.Fatal("moved provider did not use only the backup's key")
	}
}

func TestRestoreMovedAIProviderMetadataFailureKeepsKey(t *testing.T) {
	a, _, _ := backupApp(t)
	opts, _ := installBackupStore(t, a)
	path := filepath.Join(t.TempDir(), "hosts.json")
	var err error
	if a.store, err = assets.Open(path); err != nil {
		t.Fatal(err)
	}
	addCred(t, a, "before", "old-secret")
	withAIProvider(t, a, "https://llm.example.com/v1")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := a.restoreBackup(en, movedProviderBackup(a, "https://llm.example.net/v1", nil)); err == nil {
		t.Fatal("restore ignored metadata write failure")
	}
	if err := a.kc.Close(); err != nil {
		t.Fatal(err)
	}
	if a.kc, err = keychain.Open(opts); err != nil {
		t.Fatal(err)
	}
	if got, err := a.kc.Get(keychain.AIKey("prov1")); err != nil || string(got) != localAIKey {
		t.Fatal("failed restore did not put the dropped AI key back")
	}
	if got := a.providerByID("prov1").BaseURL; got != "https://llm.example.com/v1" {
		t.Fatalf("failed restore moved the provider to %q", got)
	}
}

func TestRestoreMovedAIProviderSettingsFailureKeepsKey(t *testing.T) {
	a, _, _ := backupApp(t)
	withAIProvider(t, a, "https://llm.example.com/v1")
	// Settings can no longer be saved over a non-empty directory.
	path := filepath.Join(a.p.DataDir, "settings.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "blocked"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := a.restoreBackup(en, movedProviderBackup(a, "https://llm.example.net/v1", nil)); err != nil {
		t.Fatal(err)
	}
	if got := a.providerByID("prov1").BaseURL; got != "https://llm.example.com/v1" {
		t.Fatalf("settings failure still moved the provider to %q", got)
	}
	if got, err := a.kc.Get(keychain.AIKey("prov1")); err != nil || string(got) != localAIKey {
		t.Fatal("settings failure left the provider without its key")
	}
}

func TestSameAIEndpoint(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{"https://api.example.com/v1", "https://api.example.com/v2", true},
		{"https://API.example.com/v1", "HTTPS://api.EXAMPLE.com", true},
		{"https://api.example.com/v1", "https://api.example.com:443/v1", true},
		{"http://localhost/v1", "http://localhost:80/v1", true},
		{" https://api.example.com/v1 ", "https://api.example.com/v1", true},
		{"https://[::1]:8443/v1", "https://[::1]:8443/v2", true},
		{"", "", true},
		{"https://api.example.com/v1", "http://api.example.com/v1", false},
		{"https://api.example.com/v1", "https://api.example.com:8443/v1", false},
		{"https://api.example.com/v1", "https://evil.example.com/v1", false},
		{"http://localhost:80/v1", "https://localhost:80/v1", false},
		{"", "https://api.openai.com/v1", false},
		{"https://api.openai.com/v1", "", false},
		{"https://user:pw@api.example.com/v1", "https://api.example.com/v1", false},
		{"ftp://api.example.com/v1", "ftp://api.example.com/v2", false},
		{"api.example.com/v1", "api.example.com/v2", false},
	} {
		if got := sameAIEndpoint(tc.a, tc.b); got != tc.same {
			t.Errorf("sameAIEndpoint(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}
