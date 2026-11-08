// Package policy grades what an AI agent wants to do by risk and decides,
// under the session's permission mode, whether SSHYard runs it, asks the
// user, or asks the review model. SSHYard is the final judge; nothing the
// agent says (its reason, its own approval settings, allow_always) changes
// a decision.
//
// In automatic review mode, read-only actions run, Critical ones ask the
// user, and a configured review model judges every other action; local
// risk grades supply metadata and audit context, not approval.
package policy

import (
	"context"
	"io"
)

// Mode is a permission mode. The zero value means "not set at this level".
type Mode string

const (
	ModeAsk    Mode = "ask"    // 询问 (default)
	ModeReview Mode = "review" // 自动审核
	ModeFull   Mode = "full"   // 完全允许 (needs explicit confirmation at every level)
)

// Valid reports whether m is one of the three modes.
func (m Mode) Valid() bool { return m == ModeAsk || m == ModeReview || m == ModeFull }

// Resolve picks the effective mode: pane (this session only) over host
// (Host.AIPolicy.Mode) over global (AIDefaultMode). Invalid or empty levels
// are skipped; with nothing valid the answer is ModeAsk.
func Resolve(global, host, pane Mode) Mode {
	for _, m := range []Mode{pane, host, global} {
		if m.Valid() {
			return m
		}
	}
	return ModeAsk
}

// Risk is an action's risk grade, ordered from least to most dangerous;
// RiskUnknown (cannot be graded) is treated at least like RiskHigh.
type Risk int

const (
	RiskReadOnly Risk = iota // 只读
	RiskLow                  // 低
	RiskMedium               // 中
	RiskHigh                 // 高
	RiskCritical             // 高（红色）
	RiskUnknown              // 无法判定
)

// Where an action runs.
type Where string

const (
	Remote Where = "remote"
	Local  Where = "local" // an external agent's own tool on this machine
)

// Category is the action class from the spec's table, e.g. "read",
// "sensitive-read", "low-write", "write", "install", "service", "delete",
// "permission", "network", "critical", "unknown", "local-read", "local".
type Category string

// Action is one thing the agent asked to do. Every field except Where and
// Tool may be empty. All of it is untrusted input.
type Action struct {
	Where Where
	// Tool is the remote tool (host_info, pane_output, list_dir, read_file,
	// search_files, write_file, run_command, and terminal_send and
	// terminal_key, which type into a terminal) or, for Local, the ACP
	// tool_call kind (read, edit, delete, move, search, execute, fetch, think,
	// other).
	Tool    string
	Command string // run_command / execute; the line or key typed
	Path    string // file tools; for Local, the local path
	Dir     string // working directory of a command
	Reason  string // the agent's stated reason, shown and audited, never trusted
}

// Scope is what the grading needs to know about the session's host.
type Scope struct {
	Home          string   // remote $HOME (absolute)
	WritableRoots []string // default {Home, "/tmp"}; writes outside are at least RiskMedium
	Sensitive     []string // extra sensitive path globs (Host.AIPolicy), added to the built-in ones
	Sandbox       string   // the external agent's empty local sandbox dir (Local actions)
}

// Grade is the result of classifying an action.
type Grade struct {
	Category Category
	Risk     Risk
	Prefix   string // command prefix or directory that "allow similar this session" matches on
	Why      string // short machine reason, e.g. "rm in pipeline", for the timeline and audit
}

// Outcome is what happens next.
type Outcome string

const (
	Allow  Outcome = "allow"  // run without asking
	Ask    Outcome = "ask"    // ask the user
	Review Outcome = "review" // ask the review model; anything but a clean "allow" becomes Ask
)

// Decider is who made the final call (audit field 裁决人).
type Decider string

const (
	ByRule     Decider = "rule"
	ByReviewer Decider = "reviewer"
	ByUser     Decider = "user"
	ByFull     Decider = "full" // 完全允许
)

// Decision is a grade plus what the mode says to do with it.
type Decision struct {
	Grade
	Mode    Mode
	Outcome Outcome
	By      Decider
	Similar bool // the confirmation may offer "allow similar this session" (never for High/Critical/Unknown)
	Diff    bool // a write: show the diff (new file: full text) before asking
	Red     bool // RiskCritical: red warning in the dialog / timeline
}

// Answer is the user's (or the system's) reply to a confirmation.
type Answer int

const (
	Reject Answer = iota // also the default focus
	AllowOnce
	AllowSimilar // this pane session only
	TimedOut     // 5 minutes without an answer: treated as Reject
	Cancelled    // Stop / session cancel
)

// Prompt is what the confirmation dialog shows.
type Prompt struct {
	Action     Action
	Decision   Decision
	Diff       string // unified diff or the new file's text; empty for non-writes
	ReviewNote string // the review model's reason, if it was consulted
}

// Prompter asks the user. It must return when ctx is done (Cancelled).
type Prompter interface {
	Ask(ctx context.Context, p Prompt) Answer
}

// ReviewInput is all the review model may see: no credentials, remote output
// only as bounded, marked untrusted data (last 20 lines).
type ReviewInput struct {
	// Task is what the user asked for in this turn, against which the
	// reviewer judges whether the action belongs. Earlier holds the
	// user's previous messages in the pane, oldest first, for the task
	// a short "continue" or "fix it" refers to.
	Task     string
	Earlier  []string
	Category Category
	Risk     Risk
	Why      string // the local rule that set the risk, e.g. "unlisted command: atop"
	Where    Where
	Dir      string // actual working directory; empty means unknown
	Command  string
	Path     string
	DiffSum  string
	HostMeta string
	Reason   string
	Output   string
}

// Reviewer is the optional review model. Any error, timeout (10 s) or
// unparsable reply must make the gate ask; it never auto-rejects.
type Reviewer interface {
	Review(ctx context.Context, in ReviewInput) (allow bool, reason string, err error)
}

// Verdict is the gate's final answer for one action.
type Verdict struct {
	Decision
	Run          bool   // execute the action
	Answer       Answer // when a prompt was shown
	ReviewReason string
	Refusal      string // when !Run: the reason sent back to the agent as the tool result
}

// Gate grades, decides, asks the reviewer and the user as needed, records
// "allow similar" grants for its session, and writes the audit record.
// One Gate per AI pane session.
type Gate interface {
	Authorize(ctx context.Context, a Action, diff string) (Verdict, error)
	// Done records the result of an action that ran (audit: exit code,
	// duration, output bytes).
	Done(a Action, v Verdict, exit int, durMS int64, outBytes int64)
}

// Exec is the seam to the remote exec channel manager from ① (feat/metrics).
// Run executes cmd with `sh -c` in its own process group on a fresh exec
// channel of the host's pooled *ssh.Client (never a second connection, no
// agent forwarding, no injected env, never the user's visible shell). When
// ctx is done it stops the command: SSH signal, then `kill -TERM -<pgid>`,
// then -KILL after 5 s. Not implemented in A1 or A2.
type Exec interface {
	Run(ctx context.Context, cmd, dir string, stdout, stderr io.Writer) (exit int, err error)
}
