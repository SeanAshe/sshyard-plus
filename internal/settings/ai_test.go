package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAIDefaults(t *testing.T) {
	s := Default()
	if s.AIDefaultAgent != "builtin" || s.AIDefaultMode != "ask" || s.AIReviewProvider != "" {
		t.Fatalf("defaults %+v", s)
	}
	if s.AIContextLines != 200 || s.AIAuditRetentionDays != 90 || !s.AIRedact || s.AIRequestTimeout != DefaultAIRequestTimeout {
		t.Fatalf("defaults %+v", s)
	}
	if len(s.AIProviders) != 0 || len(s.AIConsentHosts) != 0 {
		t.Fatalf("defaults %+v", s)
	}
}

func TestAINormalize(t *testing.T) {
	s := Default()
	s.AIDefaultAgent = "  "
	s.AIDefaultMode = "nope"
	s.AIContextLines = 3
	s.AIAuditRetentionDays = 0
	s.AIRequestTimeout = 3
	s.AIReviewProvider = "missing"
	s.AIDefaultProvider = "missing"
	s.AIProviders = []AIProvider{{ID: "p", Name: "n", BaseURL: "https://example.test/v1", Model: "m"}}
	s.Normalize()
	if s.AIDefaultAgent != "builtin" || s.AIDefaultMode != "ask" || s.AIContextLines != 50 || s.AIAuditRetentionDays != 90 || s.AIRequestTimeout != AIRequestTimeoutMin {
		t.Fatalf("normalized %+v", s)
	}
	if s.AIReviewProvider != "" {
		t.Fatalf("unknown review provider kept: %q", s.AIReviewProvider)
	}
	if s.AIDefaultProvider != "" {
		t.Fatalf("unknown default provider kept: %q", s.AIDefaultProvider)
	}
	s.AIDefaultProvider = " p "
	s.Normalize()
	if s.AIDefaultProvider != "p" {
		t.Fatalf("default provider %q", s.AIDefaultProvider)
	}

	// "full" from a file is kept; the UI is what requires the confirmation.
	s = Default()
	s.AIDefaultMode = "full"
	s.AIContextLines = 5000
	s.AIAuditRetentionDays = 30
	s.AIRequestTimeout = 99999
	s.AIRedact = false
	s.Normalize()
	if s.AIDefaultMode != "full" || s.AIContextLines != 2000 || s.AIAuditRetentionDays != 30 || s.AIRequestTimeout != AIRequestTimeoutMax || s.AIRedact {
		t.Fatalf("kept %+v", s)
	}

	// A file that never mentions the AI fields keeps the defaults.
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`{"theme":"dark"}`), 0o600)
	got, err := Load(p)
	if err != nil || got.AIDefaultAgent != "builtin" || got.AIDefaultMode != "ask" || got.AIContextLines != 200 || !got.AIRedact || got.AIAuditRetentionDays != 90 || got.AIRequestTimeout != DefaultAIRequestTimeout {
		t.Fatalf("old file %+v %v", got, err)
	}
}

func TestAIProviderRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	s := Default()
	s.AIDefaultAgent = "builtin"
	s.AIDefaultMode = "review"
	s.AIReviewProvider = "p1"
	s.AIContextLines = 80
	s.AIConsentHosts = []string{"h1"}
	s.AIProviders = []AIProvider{{
		ID: "p1", Name: "lab", BaseURL: "https://models.example.test/v1", Model: "fake-model",
		Headers: map[string]string{"X-Title": "SSHYard"}, Temperature: ptr(0.2), MaxTokens: ptr(400),
		Format: AIFormatAnthropic, Models: []string{"fake-model", "other"},
	}}
	if err := Save(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.AIDefaultMode != "review" || got.AIReviewProvider != "p1" || got.AIContextLines != 80 {
		t.Fatalf("load %+v %v", got, err)
	}
	if len(got.AIProviders) != 1 || got.AIProviders[0].Headers["X-Title"] != "SSHYard" || got.AIProviders[0].Model != "fake-model" {
		t.Fatalf("provider %+v", got.AIProviders)
	}
	if gp := got.AIProviders[0]; gp.Format != AIFormatAnthropic || *gp.Temperature != 0.2 || *gp.MaxTokens != 400 || gp.TopP != nil || len(gp.Models) != 2 || gp.LegacyMaxTokens {
		t.Fatalf("provider %+v", gp)
	}
	if len(got.AIConsentHosts) != 1 || got.AIConsentHosts[0] != "h1" {
		t.Fatalf("consent %+v", got.AIConsentHosts)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "apikey") || strings.Contains(string(b), "secret") {
		t.Fatalf("secret-shaped field in settings: %s", b)
	}
}

func TestProviderURLAndHeaders(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.test", "https://", "http://", "/v1", "https://user:pw@example.test/v1"} {
		if _, err := CheckBaseURL(raw); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
	if warn, err := CheckBaseURL("https://api.example.test/v1"); err != nil || warn != nil {
		t.Fatalf("https: warn %v err %v", warn, err)
	}
	if warn, err := CheckBaseURL("http://127.0.0.1:9/v1"); err != nil || warn != nil {
		t.Fatalf("loopback http: warn %v err %v", warn, err)
	}
	if warn, err := CheckBaseURL("http://api.example.test/v1"); err != nil || warn == nil {
		t.Fatalf("public http: warn %v err %v", warn, err)
	}

	if err := CheckHeaders(map[string]string{"X-Title": "SSHYard"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckHeaders(map[string]string{"X-Bad": "a\nb"}); err == nil {
		t.Fatal("newline header accepted")
	}
	if err := CheckHeaders(map[string]string{"Authorization": "Bearer test-key-not-real"}); err == nil {
		t.Fatal("Authorization header accepted")
	}
	if err := CheckHeaders(map[string]string{"authorization": "x"}); err == nil {
		t.Fatal("authorization header accepted")
	}

	s := Default()
	s.AIProviders = []AIProvider{{
		ID: "p", Name: "n", BaseURL: " https://api.example.test/v1 ", Model: " m ",
		Headers: map[string]string{"Authorization": "Bearer test-key-not-real", "X-Ok": "v", "X-Bad": "a\r\nb"},
	}}
	s.Normalize()
	h := s.AIProviders[0].Headers
	if s.AIProviders[0].BaseURL != "https://api.example.test/v1" || s.AIProviders[0].Model != "m" {
		t.Fatalf("trimmed %+v", s.AIProviders[0])
	}
	if _, ok := h["Authorization"]; ok {
		t.Fatalf("Authorization kept: %+v", h)
	}
	if _, ok := h["X-Bad"]; ok || h["X-Ok"] != "v" {
		t.Fatalf("headers %+v", h)
	}
}

func ptr[T any](v T) *T { return &v }

func TestAIProviderFormatAndParams(t *testing.T) {
	// A profile saved before formats: its limit went out as max_tokens,
	// and a zero temperature was never written.
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`{"aiProviders":[{"id":"a","name":"a","baseUrl":"https://x.test/v1","model":"m","maxTokens":4096},{"id":"b","name":"b","baseUrl":"https://x.test/v1","model":"m","temperature":0.7}]}`), 0o600)
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	a, b := got.AIProviders[0], got.AIProviders[1]
	if a.Format != AIFormatChat || !a.LegacyMaxTokens || a.MaxTokens == nil || *a.MaxTokens != 4096 || a.Temperature != nil {
		t.Fatalf("legacy a %+v", a)
	}
	if b.Format != AIFormatChat || b.LegacyMaxTokens || b.MaxTokens != nil || b.Temperature == nil || *b.Temperature != 0.7 {
		t.Fatalf("legacy b %+v", b)
	}

	s := Default()
	s.AIProviders = []AIProvider{{ID: "p", Format: "nope", Temperature: ptr(3.0), TopP: ptr(-1.0), MaxTokens: ptr(0), LegacyMaxTokens: true,
		Models: []string{" a ", "a", "", "b"}, Headers: map[string]string{"x-api-key": "k", "X-Ok": "v"}},
		{ID: "q", Format: AIFormatResponses, MaxTokens: ptr(AIMaxTokensMax + 1), LegacyMaxTokens: true}}
	s.Normalize()
	pp, q := s.AIProviders[0], s.AIProviders[1]
	if pp.Format != AIFormatChat || pp.Temperature != nil || pp.TopP != nil || pp.MaxTokens != nil || !pp.LegacyMaxTokens {
		t.Fatalf("normalized %+v", pp)
	}
	if strings.Join(pp.Models, ",") != "a,b" || len(pp.Headers) != 1 || pp.Headers["X-Ok"] != "v" {
		t.Fatalf("models %v headers %v", pp.Models, pp.Headers)
	}
	if q.LegacyMaxTokens || *q.MaxTokens != AIMaxTokensMax {
		t.Fatalf("responses %+v", q)
	}
	if err := CheckHeaders(map[string]string{"X-API-Key": "k"}); err == nil {
		t.Fatal("x-api-key header accepted")
	}
}

// TestAIReviewSettings checks that a review keeps a kind its provider can
// give, that an old review setting asks the chat model, and that a
// provider that only decides is never the default for panes.
func TestAIReviewSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`{"aiReviewProvider":"oa","aiDefaultProvider":"ts","aiProviders":[
		{"id":"oa","name":"OpenAI","format":"responses","baseUrl":"https://api.openai.com/v1","model":"gpt-5"},
		{"id":"ts","name":"TypeSafe","format":"typesafe","baseUrl":"https://api.typesafe.ai/v1","model":"jev-latest","temperature":0.5}]}`), 0o600)
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.AIReviewKind != AIReviewChat || got.AIReviewStrictness != AIReviewStandard || got.AIDefaultProvider != "" {
		t.Fatalf("review %q %q default %q", got.AIReviewKind, got.AIReviewStrictness, got.AIDefaultProvider)
	}
	ts := got.AIProviders[1]
	if ts.Format != AIFormatTypeSafe || ts.Temperature != nil || ts.CanChat() {
		t.Fatalf("typesafe %+v", ts)
	}
	for _, c := range []struct{ provider, kind, model, wantKind, wantModel string }{
		{"ts", AIReviewChat, "gpt-5", AIReviewJev, ""},
		{"ts", AIReviewJev, "jev-preview", AIReviewJev, "jev-preview"},
		{"oa", AIReviewDecisions, "gpt-6-luna", AIReviewDecisions, "gpt-6-luna"},
		{"oa", AIReviewJev, "jev-latest", AIReviewChat, ""},
		{"gone", AIReviewChat, "m", "", ""},
	} {
		s := got
		s.AIReviewProvider, s.AIReviewKind, s.AIReviewModel, s.AIReviewStrictness = c.provider, c.kind, c.model, "bogus"
		s.Normalize()
		if s.AIReviewKind != c.wantKind || s.AIReviewModel != c.wantModel || s.AIReviewStrictness != AIReviewStandard {
			t.Fatalf("%+v: kind %q model %q strictness %q", c, s.AIReviewKind, s.AIReviewModel, s.AIReviewStrictness)
		}
	}
	if ReviewThreshold(AIReviewLoose) >= ReviewThreshold(AIReviewStandard) || ReviewThreshold(AIReviewStandard) >= ReviewThreshold(AIReviewStrict) {
		t.Fatal("thresholds out of order")
	}
}

// TestMaxToolCallsSetting: unset takes the built-in 25, 0 is a choice of no
// limit that survives both normalization and a JSON round trip, and out of
// range values are brought back in.
func TestMaxToolCallsSetting(t *testing.T) {
	s := Default()
	if s.AIMaxToolCalls != nil || s.MaxToolCalls() != DefaultMaxToolCalls {
		t.Fatalf("unset %v %d", s.AIMaxToolCalls, s.MaxToolCalls())
	}
	zero := 0
	s.AIMaxToolCalls = &zero
	if s.MaxToolCalls() != 0 {
		t.Fatalf("zero %d", s.MaxToolCalls())
	}
	sixty := 60
	s.AIMaxToolCalls = &sixty
	if s.MaxToolCalls() != 60 {
		t.Fatalf("set %d", s.MaxToolCalls())
	}
	neg := -5
	s.AIMaxToolCalls = &neg
	if s.MaxToolCalls() != 0 {
		t.Fatalf("negative %d", s.MaxToolCalls())
	}
	huge := AIMaxToolCallsMax + 1
	s.AIMaxToolCalls = &huge
	if s.MaxToolCalls() != AIMaxToolCallsMax {
		t.Fatalf("above the bound %d", s.MaxToolCalls())
	}

	// 0 stays 0 through normalization: it is a saved choice, not a missing one.
	s = Default()
	zero = 0
	s.AIMaxToolCalls = &zero
	s.Normalize()
	if s.AIMaxToolCalls == nil || *s.AIMaxToolCalls != 0 {
		t.Fatalf("normalized 0 into %v", s.AIMaxToolCalls)
	}
	neg = -3
	s.AIMaxToolCalls = &neg
	s.Normalize()
	if s.AIMaxToolCalls == nil || *s.AIMaxToolCalls != 0 {
		t.Fatalf("normalized a negative into %v", s.AIMaxToolCalls)
	}
	huge = AIMaxToolCallsMax + 1
	s.AIMaxToolCalls = &huge
	s.Normalize()
	if s.AIMaxToolCalls == nil || *s.AIMaxToolCalls != AIMaxToolCallsMax {
		t.Fatalf("normalized a large limit into %v", s.AIMaxToolCalls)
	}

	// The file writes an explicit 0, so no limit survives a reload.
	s = Default()
	zero = 0
	s.AIMaxToolCalls = &zero
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"aiMaxToolCalls":0`) {
		t.Fatalf("no limit was not written: %s", data)
	}
	var back Settings
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.AIMaxToolCalls == nil || back.MaxToolCalls() != 0 {
		t.Fatalf("reloaded %v", back.AIMaxToolCalls)
	}
}
