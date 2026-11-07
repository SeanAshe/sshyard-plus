// Package shellhist reads the commands a shell kept in its history file:
// bash's, zsh's, fish's, a plain sh's and PowerShell's (PSReadLine), with
// the time each ran where the file records it.
package shellhist

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The shells whose history files are read.
const (
	Bash       = "bash"
	Zsh        = "zsh"
	Fish       = "fish"
	Sh         = "sh"
	PowerShell = "pwsh"
)

// TailBytes is how much of the end of a history file is read; MaxEntries
// how many different commands a history keeps at most, the newest.
const (
	TailBytes  = 512 << 10
	MaxEntries = 5000
)

// Entry is a command of the history: the newest time it ran (zero when
// its file keeps none), the shell of its file, and how many times the
// part read holds it.
type Entry struct {
	Cmd   string
	At    time.Time
	Shell string
	Count int
}

// File is the end of a history file as read: its shell, its bytes, and
// whether its start was cut, which leaves its first line a part.
type File struct {
	Shell string
	Data  []byte
	Cut   bool
}

// History is what a host's history files hold, the newest command first,
// and the login shell of its user ("" when unknown).
type History struct {
	Entries []Entry
	Shells  []string
	Login   string
}

// Timed reports whether any command of shell ("" any) has a time.
func (h History) Timed(shell string) bool {
	for _, e := range h.Entries {
		if (shell == "" || e.Shell == shell) && !e.At.IsZero() {
			return true
		}
	}
	return false
}

// Has reports whether the history holds commands of shell.
func (h History) Has(shell string) bool { return slices.Contains(h.Shells, shell) }

// Parse is the commands of a history file of shell, in the file's order;
// cut drops its first line, a part of a command.
func Parse(shell string, data []byte, cut bool) []Entry {
	if cut {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	var out []Entry
	switch shell {
	case Zsh:
		out = parseZsh(unmetafy(data))
	case Fish:
		out = parseFish(data)
	case Bash:
		out = parseBash(data)
	case PowerShell:
		out = parseContinued(data, '`')
	default:
		out = parseContinued(data, 0)
	}
	n := 0
	for _, e := range out {
		e.Cmd = clean(e.Cmd)
		if strings.TrimSpace(e.Cmd) == "" {
			continue
		}
		e.Shell, e.Count = shell, 1
		out[n] = e
		n++
	}
	return out[:n]
}

// Merge is the commands of files, the newest first, each once: where it
// ran last, with how many times it ran. A command of a file without
// times takes the time of the last timed one before it in its file, so
// that the order of each file stays; one with none sorts after the timed.
func Merge(files []File, max int) History {
	type item struct {
		e    Entry
		at   time.Time
		file int
		pos  int
	}
	var all []item
	var h History
	for fi, f := range files {
		es := Parse(f.Shell, f.Data, f.Cut)
		if len(es) > 0 && !slices.Contains(h.Shells, f.Shell) {
			h.Shells = append(h.Shells, f.Shell)
		}
		var last time.Time
		for i, e := range es {
			if !e.At.IsZero() {
				last = e.At
			}
			all = append(all, item{e, last, fi, i})
		}
	}
	// The newest first: by time, and in a file, the later line first.
	slices.SortStableFunc(all, func(x, y item) int {
		if c := y.at.Compare(x.at); c != 0 {
			return c
		}
		if x.file != y.file {
			return x.file - y.file
		}
		return y.pos - x.pos
	})
	seen := map[string]int{}
	for _, it := range all {
		key := strings.TrimSpace(it.e.Cmd)
		if i, ok := seen[key]; ok {
			h.Entries[i].Count++
			continue
		}
		if max > 0 && len(h.Entries) >= max {
			continue
		}
		seen[key] = len(h.Entries)
		h.Entries = append(h.Entries, it.e)
	}
	return h
}

// parseBash reads bash's history: a line a command, or, with times
// (HISTTIMEFORMAT), "#<seconds>" before each, the lines up to the next
// such mark being one command of several lines.
func parseBash(data []byte) []Entry {
	lines := splitLines(data)
	timed := false
	for _, l := range lines {
		if _, ok := bashStamp(l); ok {
			timed = true
			break
		}
	}
	if !timed {
		return parseContinued(data, 0)
	}
	var out []Entry
	var cur *Entry
	for _, l := range lines {
		if at, ok := bashStamp(l); ok {
			out = append(out, Entry{At: at})
			cur = &out[len(out)-1]
			continue
		}
		if cur == nil {
			// lines before the first mark, from before the times
			out = append(out, Entry{Cmd: l})
			continue
		}
		if cur.Cmd == "" {
			cur.Cmd = l
		} else {
			cur.Cmd += "\n" + l
		}
	}
	return out
}

// bashStamp is the time of a "#<seconds>" line of bash's history.
func bashStamp(l string) (time.Time, bool) {
	if len(l) < 2 || l[0] != '#' {
		return time.Time{}, false
	}
	return unixTime(l[1:])
}

// parseZsh reads zsh's history: lines, plain or as ": <start>:<elapsed>;"
// before the command (EXTENDED_HISTORY), a newline in a command kept as a
// line ending in a backslash.
func parseZsh(data []byte) []Entry {
	var out []Entry
	lines := splitLines(data)
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		var e Entry
		if strings.HasPrefix(l, ": ") {
			if semi := strings.IndexByte(l, ';'); semi > 0 {
				meta := l[2:semi]
				if colon := strings.IndexByte(meta, ':'); colon > 0 {
					if at, ok := unixTime(meta[:colon]); ok {
						e.At, l = at, l[semi+1:]
					}
				}
			}
		}
		for strings.HasSuffix(l, `\`) && i+1 < len(lines) {
			i++
			l = l[:len(l)-1] + "\n" + lines[i]
		}
		e.Cmd = l
		out = append(out, e)
	}
	return out
}

// parseFish reads fish's history, entries as "- cmd: <command>" and
// "  when: <seconds>", a backslash and a newline in a command escaped.
func parseFish(data []byte) []Entry {
	var out []Entry
	for _, l := range splitLines(data) {
		switch {
		case strings.HasPrefix(l, "- cmd: "):
			out = append(out, Entry{Cmd: fishUnescape(l[len("- cmd: "):])})
		case strings.HasPrefix(l, "  when: ") && len(out) > 0:
			if at, ok := unixTime(strings.TrimSpace(l[len("  when: "):])); ok {
				out[len(out)-1].At = at
			}
		}
	}
	return out
}

func fishUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case '\\':
				b.WriteByte('\\')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// parseContinued reads a line a command, without times; a line ending in
// cont (PowerShell's backtick) goes on on the next one. 0 for none.
func parseContinued(data []byte, cont byte) []Entry {
	var out []Entry
	lines := splitLines(data)
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		for cont != 0 && strings.HasSuffix(l, string(cont)) && i+1 < len(lines) {
			i++
			l = l[:len(l)-1] + "\n" + lines[i]
		}
		out = append(out, Entry{Cmd: l})
	}
	return out
}

// splitLines is data's lines, without their ends.
func splitLines(data []byte) []string {
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// unixTime reads seconds since 1970, from 2000 on: a number before is no
// time a shell wrote.
func unixTime(s string) (time.Time, bool) {
	if s == "" || len(s) > 12 {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 946684800 {
		return time.Time{}, false
	}
	return time.Unix(n, 0), true
}

// unmetafy undoes zsh's meta encoding: a byte 0x83, then a byte xor 32.
func unmetafy(data []byte) []byte {
	if bytes.IndexByte(data, 0x83) < 0 {
		return data
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == 0x83 && i+1 < len(data) {
			i++
			out = append(out, data[i]^32)
			continue
		}
		out = append(out, data[i])
	}
	return out
}

// clean is cmd as text: invalid UTF-8 replaced, and control characters
// other than tabs and newlines dropped, so that it can neither draw
// nor send what the shell never ran.
func clean(cmd string) string {
	if !utf8.ValidString(cmd) {
		cmd = strings.ToValidUTF8(cmd, "�")
	}
	return strings.Map(func(r rune) rune {
		if r == 0x7f || r < 0x20 && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, cmd)
}

// Candidate is a history file a shell may keep, under the home folder.
type Candidate struct {
	Shell string
	Path  string
}

// LocalCandidates are the history files of this computer's user: on
// goos with home, $ZDOTDIR, $XDG_DATA_HOME and %APPDATA% as getenv has them.
func LocalCandidates(goos, home string, getenv func(string) string) []Candidate {
	if goos == "windows" {
		app := getenv("APPDATA")
		if app == "" {
			app = filepath.Join(home, "AppData", "Roaming")
		}
		return []Candidate{{PowerShell, filepath.Join(app, "Microsoft", "Windows", "PowerShell", "PSReadLine", "ConsoleHost_history.txt")}}
	}
	zdot := getenv("ZDOTDIR")
	if zdot == "" {
		zdot = home
	}
	data := getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	out := []Candidate{
		{Zsh, filepath.Join(zdot, ".zsh_history")},
		{Zsh, filepath.Join(home, ".zhistory")},
		{Bash, filepath.Join(home, ".bash_history")},
		{Fish, filepath.Join(data, "fish", "fish_history")},
		{Sh, filepath.Join(home, ".sh_history")},
		{PowerShell, filepath.Join(home, ".local", "share", "powershell", "PSReadLine", "ConsoleHost_history.txt")},
	}
	if zdot != home {
		out = append(out, Candidate{Zsh, filepath.Join(home, ".zsh_history")})
	}
	return out
}

// ReadLocal reads this computer's history files, those of cands there.
func ReadLocal(cands []Candidate, login string) History {
	var files []File
	seen := map[string]bool{}
	for _, cd := range cands {
		if seen[cd.Path] {
			continue
		}
		seen[cd.Path] = true
		if f, ok := readTail(cd); ok {
			files = append(files, f)
		}
	}
	h := Merge(files, MaxEntries)
	h.Login = login
	return h
}

func readTail(cd Candidate) (File, bool) {
	f, err := os.Open(cd.Path)
	if err != nil {
		return File{}, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return File{}, false
	}
	off := max(st.Size()-TailBytes, 0)
	buf := make([]byte, st.Size()-off)
	n, _ := f.ReadAt(buf, off)
	return File{Shell: cd.Shell, Data: buf[:n], Cut: off > 0}, true
}

// LoginShell names the shell of a login shell's path, "" for none known.
func LoginShell(path string) string {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(strings.ReplaceAll(path, `\`, "/"))), ".exe")
	switch base {
	case "bash", "zsh", "fish":
		return base
	case "pwsh", "powershell":
		return PowerShell
	case "sh", "ash", "dash", "ksh", "mksh":
		return Sh
	}
	return ""
}
