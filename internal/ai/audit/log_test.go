package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Licoy/sshyard/internal/ai/policy"
)

func TestNormalizeRetention(t *testing.T) {
	if NormalizeRetention(0) != DefaultRetentionDays || NormalizeRetention(-5) != 90 || NormalizeRetention(10) != 10 {
		t.Fatalf("%d %d %d", NormalizeRetention(0), NormalizeRetention(-5), NormalizeRetention(10))
	}
}

func TestLogRoundTrip(t *testing.T) {
	dir := t.TempDir()
	log := New(dir)
	zone := time.FixedZone("CST", 8*3600)
	when := time.Date(2026, 10, 27, 15, 4, 5, 0, zone)
	code := 7
	rec := policy.AuditRecord{
		Time: when, SessionID: "sess", PaneID: "pane", Agent: "agent", Host: "host.example",
		Mode: "ask", Where: "remote", Category: "read", Risk: "readonly",
		Command: "ls && ls", DiffHash: "", DiffLines: policy.DiffLines{},
		Verdict: "auto", Decider: "rule", ReviewReason: "",
		ExitCode: &code, DurationMS: 12, OutputBytes: 4,
	}
	if err := log.Record(rec); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ai-audit", "2026-10.jsonl")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "2026-10-27T15:04:05+08:00") {
		t.Fatalf("time: %s", body)
	}
	if strings.Contains(string(body), `\u0026`) {
		t.Fatalf("escaped ampersand: %s", body)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(body), &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"time", "session_id", "pane_id", "agent", "host", "mode", "where",
		"category", "risk", "command", "diff_hash", "diff_lines", "verdict",
		"decider", "review_reason", "exit_code", "duration_ms", "output_bytes",
	} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("missing %s in %s", key, body)
		}
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %o", st.Mode().Perm())
		}
		dst, err := os.Stat(filepath.Join(dir, "ai-audit"))
		if err != nil {
			t.Fatal(err)
		}
		if dst.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %o", dst.Mode().Perm())
		}
	}
	got, err := log.Read(Filter{})
	if err != nil || len(got) != 1 || got[0].SessionID != "sess" || got[0].Command != "ls && ls" || got[0].ExitCode == nil || *got[0].ExitCode != 7 {
		t.Fatalf("%+v %v", got, err)
	}
	if !got[0].Time.Equal(when) {
		t.Fatalf("time %s", got[0].Time)
	}
}

func TestLogPruneFilterExport(t *testing.T) {
	dir := t.TempDir()
	log := New(dir)
	zone := time.FixedZone("CST", 8*3600)
	write := func(host string, at time.Time) {
		t.Helper()
		if err := log.Record(policy.AuditRecord{
			Time: at, Host: host, SessionID: "s", PaneID: "p", Agent: "a",
			Mode: "ask", Where: "remote", Category: "read", Risk: "readonly",
			Command: "ls", Verdict: "auto", Decider: "rule",
		}); err != nil {
			t.Fatal(err)
		}
	}
	write("keep", time.Date(2026, 6, 15, 12, 0, 0, 0, zone))
	write("keep", time.Date(2026, 7, 15, 12, 0, 0, 0, zone))
	write("web", time.Date(2026, 10, 1, 0, 0, 0, 0, zone))
	write("db", time.Date(2026, 10, 27, 12, 0, 0, 0, zone))
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, zone)
	if err := log.Prune(now, 90); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ai-audit", "2026-06.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("june still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ai-audit", "2026-07.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ai-audit", "2026-10.jsonl")); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(dir, "ai-audit", "notes.txt")
	if err := os.WriteFile(note, []byte("leave me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := log.Prune(now, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(note); err != nil {
		t.Fatal(err)
	}
	month := filepath.Join(dir, "ai-audit", "2026-10.jsonl")
	f, err := os.OpenFile(month, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\nnot json\n{\"time\":\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got, err := log.Read(Filter{Host: "web"})
	if err != nil || len(got) != 1 || got[0].Host != "web" {
		t.Fatalf("%+v %v", got, err)
	}
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, zone)
	to := time.Date(2026, 10, 27, 12, 0, 0, 0, zone)
	got, err = log.Read(Filter{From: from, To: to})
	if err != nil || len(got) != 1 || got[0].Host != "db" {
		t.Fatalf("bounds %+v %v", got, err)
	}
	var buf bytes.Buffer
	if err := log.Export(&buf, Filter{Host: "db"}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "\n") != 1 || !strings.Contains(buf.String(), `"host":"db"`) {
		t.Fatalf("export %s", buf.String())
	}
}

func TestLogConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	log := New(dir)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				err := log.Record(policy.AuditRecord{
					Time: time.Date(2026, 10, 2, 0, 0, i, j, time.UTC),
					Host: "h", SessionID: "s", Command: "ls",
					Verdict: "auto", Decider: "rule", Risk: "readonly",
				})
				if err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
	got, err := log.Read(Filter{})
	if err != nil || len(got) != 160 {
		t.Fatalf("len %d err %v", len(got), err)
	}
}

func TestGateSecretsDoNotReachTheFile(t *testing.T) {
	dir := t.TempDir()
	log := New(dir)
	zone := time.FixedZone("CST", 8*3600)
	when := time.Date(2026, 10, 27, 15, 4, 5, 0, zone)
	g := policy.NewGate(policy.GateConfig{
		Auditor:   log,
		Now:       func() time.Time { return when },
		Mode:      func() policy.Mode { return policy.ModeAsk },
		Scope:     policy.Scope{Home: "/home/user"},
		SessionID: "sess",
		PaneID:    "pane",
		Agent:     "agent",
		Host:      "host.example",
		Output:    func() string { return "UNIQUE_OUTPUT_TEXT" },
	})
	payload := strings.Join([]string{
		"-----BEGIN OPENSSH PRIVATE KEY-----\nZmFrZS1rZXktZm9yLXRlc3RzLW9ubHk=\n-----END OPENSSH PRIVATE KEY-----",
		"Bearer fake-token-not-real",
		"AKIAFAKEFAKEFAKEFAKE",
		"password=fake-password-value",
		"mysql -pfake-db-pass",
		"https://user:fake-pass@example.com",
	}, " ")
	command := "echo " + strconv.Quote(payload)
	diff := "+UNIQUE_DIFF_TEXT_SHOULD_NOT_LEAK\n"
	v, err := g.Authorize(context.Background(), policy.Action{
		Where: policy.Remote, Tool: "run_command", Command: command,
	}, diff)
	if err != nil || !v.Run {
		t.Fatalf("%+v %v", v, err)
	}
	if err := g.Done(policy.Action{Where: policy.Remote, Tool: "run_command", Command: command}, v, 0, 9, 11); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "ai-audit", "2026-10.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, secret := range []string{
		"ZmFrZS1rZXktZm9yLXRlc3RzLW9ubHk=",
		"fake-token-not-real",
		"AKIAFAKEFAKEFAKEFAKE",
		"fake-password-value",
		"fake-db-pass",
		"fake-pass",
		"UNIQUE_DIFF_TEXT_SHOULD_NOT_LEAK",
		"UNIQUE_OUTPUT_TEXT",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("leaked %q in %s", secret, text)
		}
	}
	for _, keep := range []string{"[REDACTED-PRIVATE-KEY]", "Bearer [REDACTED]", "[REDACTED-AWS-KEY]", "password=[REDACTED]", "user:[REDACTED]@"} {
		if !strings.Contains(text, keep) {
			t.Fatalf("missing %s in %s", keep, text)
		}
	}
	sum := sha256.Sum256([]byte(diff))
	if !strings.Contains(text, hex.EncodeToString(sum[:])) {
		t.Fatalf("missing hash in %s", text)
	}
	if !strings.Contains(text, `"output_bytes":11`) || !strings.Contains(text, `"duration_ms":9`) {
		t.Fatalf("%s", text)
	}
}

func TestLogTightensExistingMode(t *testing.T) {
	dir := t.TempDir()
	auditDir := filepath.Join(dir, "ai-audit")
	if err := os.MkdirAll(auditDir, 0o755); err != nil {
		t.Fatal(err)
	}
	month := filepath.Join(auditDir, "2026-10.jsonl")
	if err := os.WriteFile(month, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(auditDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(month, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	log := New(dir)
	if err := log.Record(policy.AuditRecord{
		Time: time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC),
		Host: "h", Command: "ls", Verdict: "auto", Decider: "rule",
	}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	st, err := os.Stat(month)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %o", st.Mode().Perm())
	}
	dst, err := os.Stat(auditDir)
	if err != nil {
		t.Fatal(err)
	}
	if dst.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %o", dst.Mode().Perm())
	}
}

func TestRejectExitCodeIsNull(t *testing.T) {
	dir := t.TempDir()
	log := New(dir)
	p := rejectPrompt{}
	g := policy.NewGate(policy.GateConfig{
		Auditor:  log,
		Mode:     func() policy.Mode { return policy.ModeAsk },
		Scope:    policy.Scope{Home: "/home/user"},
		Prompter: p,
		Now:      func() time.Time { return time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC) },
	})
	v, err := g.Authorize(context.Background(), policy.Action{
		Where: policy.Remote, Tool: "run_command", Command: "mkdir -p /tmp/x",
	}, "")
	if err != nil || v.Run {
		t.Fatalf("%+v %v", v, err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "ai-audit", "2026-10.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"exit_code":null`) || !strings.Contains(string(body), `"verdict":"reject"`) {
		t.Fatalf("%s", body)
	}
	if !strings.Contains(string(body), "2026-10-02T00:00:00+00:00") {
		t.Fatalf("offset: %s", body)
	}
}

type rejectPrompt struct{}

func (rejectPrompt) Ask(context.Context, policy.Prompt) policy.Answer { return policy.Reject }

func auditGate(log *Log, when time.Time) policy.Gate {
	return policy.NewGate(policy.GateConfig{
		Auditor: log,
		Now:     func() time.Time { return when },
		Mode:    func() policy.Mode { return policy.ModeFull },
		Host:    "h",
	})
}

func TestPrepareOpensTheMonthFile(t *testing.T) {
	dir := t.TempDir()
	log := New(dir)
	when := time.Date(2026, 10, 27, 15, 4, 5, 0, time.UTC)
	if err := log.Prepare(when); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "ai-audit", "2026-10.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %o", st.Mode().Perm())
	}
	recs, err := log.Read(Filter{})
	if err != nil || len(recs) != 0 {
		t.Fatalf("%+v %v", recs, err)
	}
}

// A month file that cannot be opened refuses the run, though the directory
// is fine, and the reason carries no local path.
func TestUnwritableMonthFileRefusesTheRun(t *testing.T) {
	dir := t.TempDir()
	month := filepath.Join(dir, "ai-audit", "2026-10.jsonl")
	if err := os.MkdirAll(month, 0o700); err != nil {
		t.Fatal(err)
	}
	g := auditGate(New(dir), time.Date(2026, 10, 27, 15, 4, 5, 0, time.UTC))
	v, err := g.Authorize(context.Background(), policy.Action{Where: policy.Remote, Tool: "run_command", Command: "ls"}, "")
	var ae *policy.AuditError
	if v.Run || !errors.As(err, &ae) || ae.Ran {
		t.Fatalf("%+v %v", v, err)
	}
	if v.Refusal == "" || strings.Contains(v.Refusal, dir) || strings.Contains(err.Error(), dir) {
		t.Fatalf("refusal %q err %q", v.Refusal, err)
	}
	if code, ok := policy.AuditNotice(err); !ok || !strings.HasPrefix(code, "audit-unavailable:") || strings.Contains(code, dir) {
		t.Fatalf("%q %v", code, ok)
	}
}

// A write that fails after the run is returned; the run is neither undone
// nor repeated.
func TestRecordFailureAfterRunIsReported(t *testing.T) {
	dir := t.TempDir()
	log := New(dir)
	g := auditGate(log, time.Date(2026, 10, 27, 15, 4, 5, 0, time.UTC))
	act := policy.Action{Where: policy.Remote, Tool: "run_command", Command: "ls"}
	v, err := g.Authorize(context.Background(), act, "")
	if err != nil || !v.Run {
		t.Fatalf("%+v %v", v, err)
	}
	month := filepath.Join(dir, "ai-audit", "2026-10.jsonl")
	if err := os.Remove(month); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(month, 0o700); err != nil {
		t.Fatal(err)
	}
	err = g.Done(act, v, 0, 5, 7)
	var ae *policy.AuditError
	if !errors.As(err, &ae) || !ae.Ran || strings.Contains(err.Error(), dir) {
		t.Fatalf("%v", err)
	}
	if code, ok := policy.AuditNotice(err); !ok || !strings.HasPrefix(code, "audit-failed:") {
		t.Fatalf("%q %v", code, ok)
	}
}
