// Package audit is the local JSONL log of AI actions.
// Files live in <dataDir>/ai-audit/YYYY-MM.jsonl, one JSON object per line.
// A line never contains file contents, command output, or secrets.
// A month file is deleted only when that whole month is older than the
// retention window (default 90 days). On open, an existing month file is
// tightened to 0600 and the directory to 0700 (Windows has no mode bits).
package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Licoy/sshyard/internal/ai/policy"
)

// DefaultRetentionDays is the retention window when the caller passes
// zero or a negative number.
const DefaultRetentionDays = 90

// NormalizeRetention returns days, or DefaultRetentionDays when days <= 0.
func NormalizeRetention(days int) int {
	if days <= 0 {
		return DefaultRetentionDays
	}
	return days
}

// Filter selects records. A zero time is unbounded. Host is exact;
// empty matches every host. From and To are inclusive.
type Filter struct {
	Host string
	From time.Time
	To   time.Time
}

// Log appends audit records. It is safe for concurrent Record calls.
type Log struct {
	dir string
	mu  sync.Mutex
}

// New writes under dataDir/ai-audit.
func New(dataDir string) *Log {
	return &Log{dir: filepath.Join(dataDir, "ai-audit")}
}

// Prepare opens for appending, creating it when needed, the month file of
// a record made at the given time. The gate calls it before a run, so a
// log that cannot be written refuses the run rather than losing its record
// afterwards.
func (l *Log) Prepare(at time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := l.open(at)
	if err != nil {
		return err
	}
	return f.Close()
}

// Record appends one line to the month file of r.Time (in that time's zone).
func (l *Log) Record(r policy.AuditRecord) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := l.open(r.Time)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(toLine(r)); err != nil {
		f.Close()
		return err
	}
	// Close can report a write the file system deferred.
	return f.Close()
}

func (l *Log) open(at time.Time) (*os.File, error) {
	if err := l.ready(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(l.monthPath(at), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	// OpenFile applies 0600 only when it creates the file.
	if err := tighten(f, 0o600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func (l *Log) ready() error {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	return tightenPath(l.dir, 0o700)
}

func tighten(f *os.File, mode os.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return f.Chmod(mode)
}

func tightenPath(path string, mode os.FileMode) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(path, mode)
}

// Read returns matching records. A malformed line is skipped.
func (l *Log) Read(f Filter) ([]policy.AuditRecord, error) {
	recs, _, err := l.collect(f)
	return recs, err
}

// Export writes matching lines as JSONL. Malformed lines are skipped.
func (l *Log) Export(w io.Writer, f Filter) error {
	_, raw, err := l.collect(f)
	if err != nil {
		return err
	}
	for _, line := range raw {
		if _, err := w.Write(line); err != nil {
			return err
		}
	}
	return nil
}

// Prune deletes month files whose next month starts at or before now-days.
// days <= 0 uses DefaultRetentionDays. Other files are left in place.
func (l *Log) Prune(now time.Time, days int) error {
	days = NormalizeRetention(days)
	cutoff := now.AddDate(0, 0, -days)
	l.mu.Lock()
	defer l.mu.Unlock()
	names, err := l.monthFiles()
	if err != nil {
		return err
	}
	for _, name := range names {
		year, month := monthOf(name)
		next := time.Date(year, time.Month(month)+1, 1, 0, 0, 0, 0, now.Location())
		if next.After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(l.dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (l *Log) monthPath(t time.Time) string {
	return filepath.Join(l.dir, t.Format("2006-01")+".jsonl")
}

type line struct {
	Time         string           `json:"time"`
	SessionID    string           `json:"session_id"`
	PaneID       string           `json:"pane_id"`
	Agent        string           `json:"agent"`
	Host         string           `json:"host"`
	Mode         string           `json:"mode"`
	Where        string           `json:"where"`
	Category     string           `json:"category"`
	Risk         string           `json:"risk"`
	Command      string           `json:"command"`
	DiffHash     string           `json:"diff_hash"`
	DiffLines    policy.DiffLines `json:"diff_lines"`
	Verdict      string           `json:"verdict"`
	Decider      string           `json:"decider"`
	ReviewReason string           `json:"review_reason"`
	ExitCode     *int             `json:"exit_code"`
	DurationMS   int64            `json:"duration_ms"`
	OutputBytes  int64            `json:"output_bytes"`
}

func toLine(r policy.AuditRecord) line {
	return line{
		Time:         r.Time.Format("2006-01-02T15:04:05-07:00"),
		SessionID:    r.SessionID,
		PaneID:       r.PaneID,
		Agent:        r.Agent,
		Host:         r.Host,
		Mode:         r.Mode,
		Where:        r.Where,
		Category:     r.Category,
		Risk:         r.Risk,
		Command:      r.Command,
		DiffHash:     r.DiffHash,
		DiffLines:    r.DiffLines,
		Verdict:      r.Verdict,
		Decider:      r.Decider,
		ReviewReason: r.ReviewReason,
		ExitCode:     r.ExitCode,
		DurationMS:   r.DurationMS,
		OutputBytes:  r.OutputBytes,
	}
}

func (l *Log) collect(f Filter) ([]policy.AuditRecord, [][]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	names, err := l.monthFiles()
	if err != nil {
		return nil, nil, err
	}
	var recs []policy.AuditRecord
	var raws [][]byte
	for _, name := range names {
		fh, err := os.Open(filepath.Join(l.dir, name))
		if err != nil {
			return nil, nil, err
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			raw := bytes.TrimSpace(sc.Bytes())
			if len(raw) == 0 {
				continue
			}
			rec, ok := parseLine(raw)
			if !ok || !match(f, rec) {
				continue
			}
			cp := make([]byte, len(raw)+1)
			copy(cp, raw)
			cp[len(raw)] = '\n'
			recs = append(recs, rec)
			raws = append(raws, cp)
		}
		err = sc.Err()
		fh.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	return recs, raws, nil
}

func match(f Filter, r policy.AuditRecord) bool {
	if f.Host != "" && r.Host != f.Host {
		return false
	}
	if !f.From.IsZero() && r.Time.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && r.Time.After(f.To) {
		return false
	}
	return true
}

func parseLine(raw []byte) (policy.AuditRecord, bool) {
	var ln line
	if err := json.Unmarshal(raw, &ln); err != nil || ln.Time == "" {
		return policy.AuditRecord{}, false
	}
	t, err := time.Parse(time.RFC3339, ln.Time)
	if err != nil {
		return policy.AuditRecord{}, false
	}
	return policy.AuditRecord{
		Time:         t,
		SessionID:    ln.SessionID,
		PaneID:       ln.PaneID,
		Agent:        ln.Agent,
		Host:         ln.Host,
		Mode:         ln.Mode,
		Where:        ln.Where,
		Category:     ln.Category,
		Risk:         ln.Risk,
		Command:      ln.Command,
		DiffHash:     ln.DiffHash,
		DiffLines:    ln.DiffLines,
		Verdict:      ln.Verdict,
		Decider:      ln.Decider,
		ReviewReason: ln.ReviewReason,
		ExitCode:     ln.ExitCode,
		DurationMS:   ln.DurationMS,
		OutputBytes:  ln.OutputBytes,
	}, true
}

var monthFile = regexp.MustCompile(`^(\d{4})-(\d{2})\.jsonl$`)

func (l *Log) monthFiles() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !validMonth(e.Name()) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

func validMonth(name string) bool {
	m := monthFile.FindStringSubmatch(name)
	if m == nil {
		return false
	}
	month, err := strconv.Atoi(m[2])
	return err == nil && month >= 1 && month <= 12
}

func monthOf(name string) (int, int) {
	m := monthFile.FindStringSubmatch(name)
	year, _ := strconv.Atoi(m[1])
	month, _ := strconv.Atoi(m[2])
	return year, month
}
