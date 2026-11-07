package app

import (
	"path/filepath"
	"testing"

	"github.com/Licoy/sshyard/internal/settings"
	"github.com/Licoy/sshyard/locales"
)

func TestAIPermissionApprovalSection(t *testing.T) {
	a, tt := newTestApp(t)
	tt.SetSize(1200, 3200)
	s := a.Settings()
	s.AIProviders = []settings.AIProvider{{ID: "review", Name: "Review test", BaseURL: "https://example.test/v1", Model: "chat-model"}}
	s.AIReviewProvider, s.AIReviewKind, s.AIReviewModel = "review", settings.AIReviewDecisions, "custom-review"
	if err := a.ApplySettings(s); err != nil {
		t.Fatal(err)
	}
	a.review.custom = true
	a.openSettings(prefAI)
	tt.Frame()
	m := locales.In("en")
	mustClick(t, tt, m.AiReviewStrict())
	tt.Frame()
	mustClick(t, tt, m.AiModeReview())
	tt.Frame()
	saved, err := settings.Load(filepath.Join(a.p.DataDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if saved.AIDefaultMode != "review" || saved.AIReviewStrictness != settings.AIReviewStrict || saved.AIReviewProvider != "review" || saved.AIReviewModel != "custom-review" || saved.AIReviewKind != settings.AIReviewDecisions {
		t.Fatalf("approval controls did not save settings: %+v", saved)
	}
}
