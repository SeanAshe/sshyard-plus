package migration

import (
	"strings"
	"unicode"
)

// parseText reads one session per line of key=value fields separated by
// blanks. Blank lines, # comments and lines without a field, such as
// separators, are skipped; authType is ignored and other keys reported.
func parseText(data []byte, _ string) (Result, error) {
	var r Result
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		fields, unclosed := textFields(line)
		if len(fields) == 0 {
			continue
		}
		e := textEntry(fields)
		if unclosed != "" {
			iniIssue(&e, "unclosed-quote", unclosed)
		}
		r.Entries = append(r.Entries, e)
	}
	return r, nil
}

type textField struct{ key, value string }

// textEntry is the session a line's fields, or a CSV row's cells under
// their column names, describe: host / ip, port, user / username,
// password and title / name / label, keys ignoring case; authType is
// ignored and other keys reported.
func textEntry(fields []textField) Entry {
	var e Entry
	var unknown []string
	for _, f := range fields {
		switch strings.ToLower(f.key) {
		case "host", "ip":
			e.Address = f.value
		case "port":
			e.Port = iniPort(f.value)
		case "user", "username":
			e.User = f.value
		case "password":
			e.Password = []byte(f.value)
		case "title", "name", "label":
			e.Name = f.value
		case "authtype":
		default:
			unknown = append(unknown, f.key)
		}
	}
	for _, key := range unknown {
		iniIssue(&e, "unsupported-field", key)
	}
	return e
}

// textFields splits a line into key=value fields; words without = are
// skipped. unclosed is the key whose quoted value runs to the line's end.
func textFields(line string) (fields []textField, unclosed string) {
	for {
		line = strings.TrimLeftFunc(line, unicode.IsSpace)
		if line == "" {
			return fields, ""
		}
		i := strings.IndexFunc(line, func(r rune) bool { return r == '=' || unicode.IsSpace(r) })
		if i < 0 {
			return fields, ""
		}
		if line[i] != '=' || i == 0 {
			// Skip the word, or the = of one without a key.
			line = line[max(i, 1):]
			continue
		}
		key := line[:i]
		value, rest, ok := textValue(line[i+1:])
		fields = append(fields, textField{key, value})
		if !ok {
			return fields, key
		}
		line = rest
	}
}

// textValue reads the value s starts with. A value opening with a quote
// holds blanks up to the matching quote, where in double quotes \" and \\
// stand for " and \; elsewhere every character, quote or backslash, is
// literal, so an unquoted password needs no escaping.
func textValue(s string) (value, rest string, ok bool) {
	word := func(s string) (string, string) {
		if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
			return s[:i], s[i:]
		}
		return s, ""
	}
	if s == "" || s[0] != '"' && s[0] != '\'' {
		value, rest = word(s)
		return value, rest, true
	}
	q := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == q:
			tail, rest := word(s[i+1:])
			return b.String() + tail, rest, true
		case q == '"' && ch == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\'):
			i++
			b.WriteByte(s[i])
		default:
			b.WriteByte(ch)
		}
	}
	return b.String(), "", false
}

// ParseText reads a host list typed or pasted for a text import, within
// the bounds Read keeps.
func ParseText(text string) (Result, error) {
	if int64(len(text)) > MaxBytes {
		return Result{}, ErrTooLarge
	}
	r, err := parseData(Text, []byte(text), "")
	if err != nil {
		return Result{}, err
	}
	if len(r.Entries) > MaxEntries {
		clearEntries(r.Entries)
		return Result{}, ErrTooLarge
	}
	for i := range r.Entries {
		validateEntry(&r.Entries[i])
	}
	if migrationIssueCount(r) > MaxIssues {
		clearEntries(r.Entries)
		return Result{}, ErrTooLarge
	}
	if len(r.Entries) == 0 {
		return Result{}, ErrNoEntries
	}
	return r, nil
}

func clearEntries(entries []Entry) {
	for _, e := range entries {
		clear(e.Password)
		clear(e.PrivateKey)
		clear(e.Passphrase)
	}
}
