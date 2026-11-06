package highlight

import (
	"regexp"
	"slices"
	"strings"
)

// Preset is a group of built-in rules the user turns on or off as one.
// Sample is a line its rules color, for the settings to show.
type Preset struct {
	ID     string
	Sample string
	rules  []rule
	// early matches before the other presets: a link holds words and
	// addresses of theirs.
	early bool
}

// The terminal's colors the presets show in.
const (
	red uint8 = iota + 1
	green
	yellow
	blue
	magenta
	cyan
	_
	gray
	_
	_
	_
	brightBlue
	brightMagenta
	brightCyan
)

// words accepts any of ws, in any case.
func words(ws ...string) func(w, lower []byte) bool {
	set := make(map[string]bool, len(ws))
	for _, w := range ws {
		set[w] = true
	}
	return func(_, lower []byte) bool { return set[string(lower)] }
}

// exact accepts any of ws, in their case.
func exact(ws ...string) func(w, lower []byte) bool {
	set := make(map[string]bool, len(ws))
	for _, w := range ws {
		set[w] = true
	}
	return func(w, _ []byte) bool { return set[string(w)] }
}

// hexWord accepts a word of n hex digits, for any of the lengths.
func hexWord(ns ...int) func(w, lower []byte) bool {
	return func(_, lower []byte) bool {
		if !slices.Contains(ns, len(lower)) {
			return false
		}
		for _, c := range lower {
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
				return false
			}
		}
		return true
	}
}

// one is a rule's single style.
func one(s Style) []Style { return []Style{s} }

// ipv6 matches an IPv6 address, with its prefix's length, in its first
// group: eight groups of hex digits, or fewer around one "::".
var ipv6 = func() string {
	h := `[0-9a-f]{1,4}`
	forms := []string{
		`(?:` + h + `:){7}` + h,
		`(?:` + h + `:){1,7}:`,
		`(?:` + h + `:){1,6}:` + h,
		`(?:` + h + `:){1,5}(?::` + h + `){1,2}`,
		`(?:` + h + `:){1,4}(?::` + h + `){1,3}`,
		`(?:` + h + `:){1,3}(?::` + h + `){1,4}`,
		`(?:` + h + `:){1,2}(?::` + h + `){1,5}`,
		h + `:(?::` + h + `){1,6}`,
		`::(?:` + h + `:){0,6}` + h,
	}
	return `(?i)(?:^|[^\w:.])((?:` + strings.Join(forms, "|") + `)(?:/\d{1,3})?)(?:$|[^\w:.])`
}()

// Presets are the built-in rules, in the order they match (the early
// ones first): an earlier group's match wins over a later's.
//
// shell is the terminal's own reading of a plain prompt and command
// output, in the order a shell line reads: who it is, its flags, then
// the numbers, brackets and prompt marks a program left uncolored.
var Presets = []Preset{
	{ID: "shell", Sample: "root@debian:~# ls -al (192.168.7.3)", rules: []rule{
		{re: regexp.MustCompile(`\b[A-Za-z0-9]+@[A-Za-z0-9\-.]+\b`), need: []string{"@"}, styles: one(Style{Color: magenta})},
		{re: regexp.MustCompile(`(?:^|\s)(-{1,2}[A-Za-z0-9_-]+)`), groups: true, need: []string{"-"}, styles: one(Style{Color: yellow})},
		{re: regexp.MustCompile(`\b\d+(?:[.:]\d+)+\b`), need: []string{".", ":"}, styles: one(Style{Color: green})},
		{re: regexp.MustCompile(`[\[\]()]`), need: []string{"(", ")", "[", "]"}, styles: one(Style{Color: green})},
		{re: regexp.MustCompile(`[#$]`), need: []string{"#", "$"}, styles: one(Style{Color: blue})},
	}},
	{ID: "errors", Sample: "ERROR connection refused: permission denied", rules: []rule{
		{word: words("error", "errors", "err", "fatal", "fail", "failed", "failure", "panic", "critical", "crit", "emerg", "emergency", "alert", "exception", "traceback", "denied", "refused", "unreachable", "segfault", "killed", "abort", "aborted"), styles: one(Style{Color: red, Bold: true})},
	}},
	{ID: "warnings", Sample: "WARNING disk almost full, retrying after timeout", rules: []rule{
		{word: words("warn", "warning", "warnings", "deprecated", "timeout", "retry", "retrying", "caution"), styles: one(Style{Color: yellow, Bold: true})},
	}},
	{ID: "info", Sample: "INFO server started  DEBUG cache hit", rules: []rule{
		{word: words("info", "notice"), styles: one(Style{Color: blue, Bold: true})},
		{word: words("debug", "trace", "verbose"), styles: one(Style{Color: gray})},
	}},
	{ID: "success", Sample: "Build succeeded: 12 passed, service active (running)", rules: []rule{
		{word: exact("OK"), styles: one(Style{Color: green, Bold: true})},
		{word: words("success", "successful", "successfully", "succeeded", "pass", "passed", "done", "complete", "completed", "active", "running", "enabled", "online", "healthy", "ready"), styles: one(Style{Color: green})},
	}},
	{ID: "network", Sample: "from 192.168.1.20:22 via fe80::1 (00:1a:2b:3c:4d:5e)", rules: []rule{
		{re: regexp.MustCompile(`\b(?:25[0-5]|2[0-4]\d|1?\d?\d)(?:\.(?:25[0-5]|2[0-4]\d|1?\d?\d)){3}(?:/\d{1,2}|:\d{1,5})?\b`), need: []string{"."}, styles: one(Style{Color: brightMagenta})},
		{re: regexp.MustCompile(`(?i)\b[0-9a-f]{2}(?::[0-9a-f]{2}){5}\b`), need: []string{":"}, styles: one(Style{Color: magenta})},
		{re: regexp.MustCompile(ipv6), groups: true, need: []string{"::"}, styles: one(Style{Color: brightMagenta})},
		{re: regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b`), need: []string{":"}, styles: one(Style{Color: brightMagenta})},
	}},
	{ID: "urls", Sample: "see https://example.com/docs?page=2", early: true, rules: []rule{
		{re: regexp.MustCompile(`\b(?:https?|ftp|ssh|wss?)://[^\s<>"'` + "`" + `]*[^\s<>"'` + "`" + `.,;:!?)\]}]`), need: []string{"://"}, styles: one(Style{Color: brightBlue, Underline: true})},
	}},
	{ID: "datetime", Sample: "2026-11-05T09:30:00Z  Nov  5 09:30:01", rules: []rule{
		{re: regexp.MustCompile(`\b\d{4}[-/]\d{2}[-/]\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2}\b)?)?`), need: []string{"-", "/"}, styles: one(Style{Color: cyan})},
		{re: regexp.MustCompile(`\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) +\d{1,2} \d{2}:\d{2}:\d{2}\b`), need: []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}, styles: one(Style{Color: cyan})},
		{re: regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b`), need: []string{":"}, styles: one(Style{Color: cyan})},
	}},
	{ID: "http", Sample: `"GET /api/items HTTP/1.1" 200  status=503`, rules: []rule{
		{word: exact("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT", "TRACE"), styles: one(Style{Color: magenta, Bold: true})},
		// A status after a request's protocol, or where a log names it,
		// by its class.
		{re: regexp.MustCompile(`(?:HTTP/[\d.]+"? |(?i:status|code)[=:] ?)(?:(2\d\d)|(3\d\d)|(4\d\d)|(5\d\d))\b`), groups: true, need: []string{"http/", "status", "code"},
			styles: []Style{{Color: green}, {Color: cyan}, {Color: yellow}, {Color: red}}},
	}},
	{ID: "ids", Sample: "id 3f2b8c1e-9d4a-4b6f-8e2a-1c5d7f9b0a3e  sha1 2fd4e1c67a2d28fced849ee1bb76e7391b93eb12", rules: []rule{
		{re: regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), need: []string{"-"}, styles: one(Style{Color: brightCyan})},
		{word: hexWord(32, 40, 64), styles: one(Style{Color: brightCyan})},
	}},
}

// PresetIDs are the IDs of the presets, all of them on by default.
func PresetIDs() []string {
	ids := make([]string, len(Presets))
	for i, p := range Presets {
		ids[i] = p.ID
	}
	return ids
}
