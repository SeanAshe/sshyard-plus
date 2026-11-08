package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/llm"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/keychain"
	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
)

func TestAIReviewUsesSelectedProviderAndNoneAsks(t *testing.T) {
	var chat, review atomic.Int32
	makeServer := func(count *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			count.Add(1)
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"choices":[{"message":{"content":"{\"decision\":\"allow\",\"reason\":\"safe\"}"},"finish_reason":"stop"}]}`)
		}))
	}
	chatServer, reviewServer := makeServer(&chat), makeServer(&review)
	defer chatServer.Close()
	defer reviewServer.Close()
	a, tt := newTestApp(t)
	h := savedHost(t, a, "review", "127.0.0.1")
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{{ID: "chat", BaseURL: chatServer.URL, Model: "chat"}, {ID: "review", BaseURL: reviewServer.URL, Model: "review"}}
	s.AIDefaultProvider = "chat"
	s.AIReviewProvider = "review"
	s.AIDefaultMode = "review"
	for _, id := range []string{"chat", "review"} {
		if err := a.kc.Set(keychain.AIKey(id), []byte("test-key-not-real")); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	ask := a.reviewAsk(c)
	if _, err := ask(t.Context(), "review"); err != nil {
		t.Fatal(err)
	}
	if chat.Load() != 0 || review.Load() != 1 {
		t.Fatalf("chat=%d review=%d", chat.Load(), review.Load())
	}
	s = a.Settings()
	s.AIReviewProvider = ""
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	gate := a.aiGate(c)
	done := make(chan policy.Verdict, 1)
	go func() {
		v, _ := gate.Authorize(context.Background(), policy.Action{Where: policy.Remote, Tool: "run_command", Command: "systemctl restart nginx"}, "")
		done <- v
	}()
	waitFor(t, tt, "user confirmation", func() bool { return c.ai.pending != nil })
	a.answerAI(c, policy.Reject)
	if v := <-done; v.Run {
		t.Fatal("None reviewer auto-authorized")
	}
	if review.Load() != 1 || chat.Load() != 0 {
		t.Fatal("None reviewer sent a model request")
	}
}

// The gate factory records the real reviewer rebuilt by ensureAI, so each
// independent settings change must refresh a live session's cached gate.
func TestAIReviewSettingsRefreshExistingSession(t *testing.T) {
	for _, field := range []string{"kind", "model", "strictness"} {
		t.Run(field, func(t *testing.T) {
			var request atomic.Value
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				request.Store(r.URL.Path + " " + string(raw))
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/decisions") {
					io.WriteString(w, decisionAnswer(0.95, "none"))
				} else {
					io.WriteString(w, `{"choices":[{"message":{"content":"{\"decision\":\"allow\",\"reason\":\"safe\"}"},"finish_reason":"stop"}]}`)
				}
			}))
			defer srv.Close()
			a, _ := newTestApp(t)
			h := savedHost(t, a, "review", "127.0.0.1")
			s := a.Settings()
			s.AIProviders = []settings.AIProvider{{ID: "p", BaseURL: srv.URL, Model: "chat"}}
			s.AIReviewProvider, s.AIReviewModel = "p", "review-old"
			s.AIReviewKind, s.AIDefaultMode = settings.AIReviewDecisions, "review"
			if field == "kind" {
				s.AIReviewKind = settings.AIReviewChat
			}
			if err := a.kc.Set(keychain.AIKey("p"), []byte("test-key-not-real")); err != nil {
				t.Fatal(err)
			}
			if err := a.ApplySettings(s); err != nil {
				t.Fatal(err)
			}
			id, err := a.OpenAI(h.ID, openAt{}, 0)
			if err != nil {
				t.Fatal(err)
			}
			c := a.conns[id]
			var gate policy.Gate
			a.newGate = func(mode func() policy.Mode, _ policy.Prompter) policy.Gate {
				gate = policy.NewGate(policy.GateConfig{Mode: mode, Reviewer: a.newReviewer(c)})
				return gate
			}
			if err := a.ensureAI(c); err != nil {
				t.Fatal(err)
			}
			session := c.ai.sess
			action := policy.Action{Where: policy.Remote, Tool: "run_command", Command: "systemctl restart nginx"}
			if v, err := gate.Authorize(t.Context(), action, ""); err != nil || !v.Run {
				t.Fatalf("initial review: %+v %v", v, err)
			}
			s = a.Settings()
			switch field {
			case "kind":
				s.AIReviewKind = settings.AIReviewDecisions
			case "model":
				s.AIReviewModel = "review-new"
			case "strictness":
				s.AIReviewStrictness = settings.AIReviewStrict
			}
			if err := a.ApplySettings(s); err != nil {
				t.Fatal(err)
			}
			if err := a.ensureAI(c); err != nil {
				t.Fatal(err)
			}
			v, err := gate.Authorize(t.Context(), action, "")
			if err != nil || v.Run != (field != "strictness") || calls.Load() != 2 || c.ai.sess != session {
				t.Fatalf("stale review: verdict=%+v error=%v calls=%d sessionKept=%v", v, err, calls.Load(), c.ai.sess == session)
			}
			body, _ := request.Load().(string)
			if !strings.HasPrefix(body, "/decisions ") || field == "model" && !strings.Contains(body, `"model":"review-new"`) {
				t.Fatalf("stale review request %q", body)
			}
		})
	}
}

func TestAIProviderDeletionInvalidatesCachedClient(t *testing.T) {
	a, _ := newTestApp(t)
	h := savedHost(t, a, "provider", "127.0.0.1")
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{{ID: "p", BaseURL: "http://127.0.0.1:1", Model: "m"}, {ID: "other", BaseURL: "http://127.0.0.1:2", Model: "m"}}
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.AIKey("p"), []byte("test-key-not-real")); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	session := c.ai.sess
	if err := a.deleteProvider("p"); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureAI(c); !errors.Is(err, errAIProvider) {
		t.Fatalf("error=%v", err)
	}
	if c.ai.sess != session {
		t.Fatal("history session replaced")
	}
	if _, err := c.ai.box.Stream(t.Context(), llm.Request{}, nil); !errors.Is(err, errAIProvider) {
		t.Fatalf("cached client survived: %v", err)
	}
}

func TestAIScopeRefreshCancelsTurnAndErrorsStayRedacted(t *testing.T) {
	a, _ := newTestApp(t)
	h := savedHost(t, a, "scope", "127.0.0.1")
	a.newModel = func(*conn) agent.Streamer { return echoModel{} }
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.ai.cancel = cancel
	h.AIPolicy.Sensitive = []string{"/private"}
	if _, err := a.store.Update(h); err != nil {
		t.Fatal(err)
	}
	a.refreshMode(c)
	if ctx.Err() == nil || !c.ai.dirty {
		t.Fatal("scope update did not cancel the current turn")
	}
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	if len(c.ai.scope.Sensitive) != 1 || c.ai.scope.Sensitive[0] != "/private" {
		t.Fatal("stale gate scope")
	}
	raw := errors.New("Authorization: Bearer secret /private/path https://user:pass@example.test")
	if text := a.scrubAI(c, raw); strings.Contains(text, "secret") || strings.Contains(text, "private") {
		t.Fatalf("unsafe error %q", text)
	}
}

func TestAIProviderKeyOnlyChangeCancelsAndSaveFailureRollsBack(t *testing.T) {
	a, _ := appIn(t, t.TempDir(), nil)
	h := savedHost(t, a, "keychange", "127.0.0.1")
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{{ID: "p", Name: "Provider", BaseURL: "https://example.test/v1", Model: "m"}}
	s.AIReviewProvider = "p"
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if err := a.kc.Set(keychain.AIKey("p"), []byte("test-old-key")); err != nil {
		t.Fatal(err)
	}
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := a.conns[id]
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.ai.cancel = cancel
	a.openProvider(&s.AIProviders[0])
	a.prov.key = "test-new-key"
	if !a.saveProvider(locales.In("en")) {
		t.Fatal(a.prov.err)
	}
	if ctx.Err() == nil || !c.ai.dirty {
		t.Fatal("key-only change did not cancel and refresh review client")
	}
	if err := a.ensureAI(c); err != nil {
		t.Fatal(err)
	}
	a.openProvider(&s.AIProviders[0])
	a.prov.key = "test-unsaved-key"
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	originalDir := a.p.DataDir
	a.p.DataDir = filepath.Join(blocker, "child")
	saved := a.saveProvider(locales.In("en"))
	a.p.DataDir = originalDir
	if saved {
		t.Fatal("settings save unexpectedly succeeded")
	}
	key, err := a.kc.Get(keychain.AIKey("p"))
	if err != nil || string(key) != "test-new-key" {
		t.Fatal("failed settings save did not restore prior key")
	}
	if a.prov.err != locales.In("en").AiSettingsSaveFailed() {
		t.Fatal("save failure did not remain visible")
	}
}

func TestAIProviderSheetFormatParamsAndModels(t *testing.T) {
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("X-Api-Key")
		io.WriteString(w, `{"data":[{"id":"claude-b"},{"id":"claude-a"},{"id":"text-embedding-x"}],"has_more":false}`)
	}))
	defer srv.Close()
	a, tt := newTestApp(t)
	tt.SetSize(1200, 3200)
	a.openSettings(prefAI)
	tt.Frame()
	mustClick(t, tt, "Add Provider")
	tt.Frame()
	f := a.prov
	if !tt.HasText("API format") || !tt.HasText("Default model") || !tt.HasText("Advanced") || tt.HasText("Top P") {
		t.Fatalf("texts %q", tt.Texts())
	}
	f.name, f.base, f.key, f.format = "lab", srv.URL+"/v1", "test-key-not-real", 2
	mustClick(t, tt, "Fetch Models")
	waitFor(t, tt, "models", func() bool { return !f.fetching })
	if f.fetchErr != "" || strings.Join(f.models, ",") != "claude-a,claude-b" || f.model != "claude-a" || gotPath != "/v1/models" || gotKey != "test-key-not-real" {
		t.Fatalf("fetch %q %v %q %s %q", f.fetchErr, f.models, f.model, gotPath, gotKey)
	}
	if !tt.HasText("Found 2 chat models") {
		t.Fatalf("texts %q", tt.Texts())
	}

	f.advanced = true
	tt.Frame()
	if !tt.HasText("Top P") || !tt.HasText("The Anthropic format requires a limit. 32000 is sent while it is unticked.") || tt.HasText("Send the limit as max_tokens (older endpoints)") {
		t.Fatalf("advanced texts %q", tt.Texts())
	}
	f.useTemp, f.temp = true, 0.4
	if !a.saveProvider(locales.In("en")) {
		t.Fatal(f.err)
	}
	p := a.Settings().AIProviders[0]
	if p.Format != settings.AIFormatAnthropic || p.Model != "claude-a" || len(p.Models) != 2 || p.Temperature == nil || *p.Temperature != 0.4 || p.TopP != nil || p.MaxTokens != nil {
		t.Fatalf("saved %+v", p)
	}

	// Reopened, the ticked parameter stays ticked and the rest stay off.
	a.openProvider(&p)
	g := a.prov
	if g.format != 2 || !g.useTemp || g.useTopP || g.useTokens || !g.advanced || len(g.models) != 2 {
		t.Fatalf("reopened %+v", g)
	}
	cl := llmClient(p, "k")
	if cl.Format != llm.FormatAnthropic || cl.Temperature == nil || cl.TopP != nil || cl.MaxTokens != 0 {
		t.Fatalf("client %+v", cl)
	}
}

func TestAIProviderTestSendsNoTokenLimit(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		io.WriteString(w, `{"choices":[{"message":{"content":"pong"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	a, tt := newTestApp(t)
	tt.SetSize(1200, 3200)
	a.openProvider(nil)
	f := a.prov
	f.name, f.base, f.model, f.key = "lab", srv.URL, "gpt-6", "test-key-not-real"
	a.testProvider()
	waitFor(t, tt, "test", func() bool { return !f.testing })
	if f.test != "ok" || strings.Contains(body, "max_") || strings.Contains(body, "temperature") {
		t.Fatalf("test %q body %s", f.test, body)
	}
}

// TestMaxToolCallsValue: the limit field reads empty as the default and
// only an explicit 0 as no limit, takes a whole number in range, and
// rejects anything else so that the setting keeps what it had.
func TestMaxToolCallsValue(t *testing.T) {
	for _, c := range []struct {
		in    string
		unset bool
		want  int
		ok    bool
	}{
		{"", true, 0, true},
		{"   ", true, 0, true},
		{"0", false, 0, true},
		{"25", false, 25, true},
		{" 30 ", false, 30, true},
		{"-1", false, 0, false},
		{"abc", false, 0, false},
		{"1.5", false, 0, false},
		{"10001", false, 0, false},
	} {
		got, ok := maxToolCallsValue(c.in)
		if ok != c.ok || ok && (got == nil) != c.unset || ok && got != nil && *got != c.want {
			t.Fatalf("%q read as %v (ok %v), want unset %v %d (ok %v)", c.in, got, ok, c.unset, c.want, c.ok)
		}
	}
	zero, sixty := 0, 60
	for n, want := range map[*int]string{nil: "", &zero: "0", &sixty: "60"} {
		if got := maxToolCallsText(n); got != want {
			t.Fatalf("%v shows as %q, want %q", n, got, want)
		}
	}
}

// TestMaxToolCallsSettingKeepsRunningTurns: the limit reaches a built-in
// pane at its next turn without stopping the one it is running, and an
// external agent, which counts its own calls, is left alone.
func TestMaxToolCallsSettingKeepsRunningTurns(t *testing.T) {
	a, _ := newTestApp(t)
	stopped := map[int]bool{}
	for id, agentID := range map[int]string{1: "builtin", 2: "acp"} {
		a.conns[id] = &conn{id: id, ai: &aiState{agent: agentID, running: true, cancel: func() { stopped[id] = true }}}
	}
	one := 1
	s := a.Settings()
	s.AIMaxToolCalls = &one
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	if stopped[1] || stopped[2] {
		t.Fatalf("changing the limit stopped turns: %v", stopped)
	}
	if !a.conns[1].ai.dirty || a.conns[2].ai.dirty {
		t.Fatalf("built-in dirty %v, external dirty %v", a.conns[1].ai.dirty, a.conns[2].ai.dirty)
	}
}

// TestMaxToolCallsSettingPausesOpenPane: lowering the cap in settings
// reaches a pane that is already open, so its next turn stops there and
// waits instead of running every call the model asks for.
func TestMaxToolCallsSettingPausesOpenPane(t *testing.T) {
	a, tt := newTestApp(t)
	h := savedHost(t, a, "web", "10.0.0.8")
	consentHost(t, a, h.ID)
	s := a.Settings()
	s.AIDefaultMode = "full" // no confirmation dialog in the way
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.newModel = func(*conn) agent.Streamer {
		return &replyModel{replies: []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "run_command", Arguments: `{"command":"uptime"}`},
			{ID: "c2", Name: "run_command", Arguments: `{"command":"uptime"}`},
		}}}}
	}
	a.newTools = func(*conn) agent.Tools { return diskTools{} }
	id, err := a.OpenAI(h.ID, openAt{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	one := 1
	s = a.Settings()
	s.AIMaxToolCalls = &one
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.SendAI(id, "check twice")
	waitFor(t, tt, "a pause at the limit", func() bool { return a.conns[id].ai.paused })
	if n := a.conns[id].ai.pauseCalls; n != 1 {
		t.Fatalf("the pause notes %d calls, want 1", n)
	}
	a.StopAI(id)
}
