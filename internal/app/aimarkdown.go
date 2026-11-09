package app

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// boldAsMeant rewrites the bold that CommonMark leaves as asterisks where
// language models mean it, which is mostly in Chinese and Japanese: a
// closing ** after punctuation and before a letter, as in **注意：**内容 or
// 压力。**以上, and an opening ** after a letter and before punctuation,
// as in 见**「设置」**. The punctuation moves outside the asterisks
// (**注意**：内容), which then pair as they read. Only lines whose ** pair
// up are touched, outside code; bold that already pairs stays as written.
func boldAsMeant(src string) string {
	if !strings.Contains(src, "**") {
		return src
	}
	lines := strings.SplitAfter(src, "\n")
	fence := ""
	changed := false
	for i, line := range lines {
		if f := codeFence(line); f != "" {
			switch {
			case fence == "":
				fence = f
			case strings.HasPrefix(f, fence) && strings.TrimSpace(strings.TrimLeft(line, " ")[len(f):]) == "":
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if fixed := boldLine(line); fixed != line {
			lines[i], changed = fixed, true
		}
	}
	if !changed {
		return src
	}
	return strings.Join(lines, "")
}

// codeFence is the ``` or ~~~ run that opens line as a code fence, or "".
func codeFence(line string) string {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 || len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return ""
	}
	n := 0
	for n < len(t) && t[n] == t[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return t[:n]
}

// boldLine is boldAsMeant on one line outside code blocks.
func boldLine(line string) string {
	var at []int
	for i := 0; i < len(line); {
		switch c := line[i]; {
		case c == '\\':
			i += 2
		case c == '`':
			n := 1
			for i+n < len(line) && line[i+n] == '`' {
				n++
			}
			i += n
			if e := codeSpanEnd(line, i, n); e >= 0 {
				i = e
			}
		case c == '*':
			n := 1
			for i+n < len(line) && line[i+n] == '*' {
				n++
			}
			if n == 2 {
				at = append(at, i)
			}
			i += n
		default:
			i++
		}
	}
	if len(at) == 0 || len(at)%2 != 0 {
		return line
	}
	var b strings.Builder
	last := 0
	for k := 0; k < len(at); k += 2 {
		o, cl := at[k], at[k+1]
		start, end := o+2, cl // the bold text
		// An opener after a letter and before punctuation: move the
		// punctuation before it.
		before, after := runeBefore(line, o), runeAt(line, start)
		if isWord(before) && isMdPunct(after) {
			e := start
			for e < end && isMdPunct(runeAt(line, e)) {
				_, w := utf8.DecodeRuneInString(line[e:])
				e += w
			}
			if e < end && isWord(runeAt(line, e)) {
				b.WriteString(line[last:o])
				b.WriteString(line[start:e])
				b.WriteString("**")
				last, start = e, e
			}
		}
		// A closer after punctuation and before a letter: move the
		// punctuation after it.
		before, after = runeBefore(line, end), runeAt(line, cl+2)
		s := end
		if isMdPunct(before) && isWord(after) {
			for s > start && isMdPunct(runeBefore(line, s)) {
				_, w := utf8.DecodeLastRuneInString(line[:s])
				s -= w
			}
			if s == start || !isWord(runeBefore(line, s)) {
				s = end
			}
		}
		b.WriteString(line[last:s])
		b.WriteString("**")
		b.WriteString(line[s:end])
		last = cl + 2
	}
	b.WriteString(line[last:])
	return b.String()
}

// codeSpanEnd is where the code span opened by n backticks before i
// ends, after its closing run of n, or -1 when it does not close.
func codeSpanEnd(line string, i, n int) int {
	for i < len(line) {
		j := strings.IndexByte(line[i:], '`')
		if j < 0 {
			return -1
		}
		i += j
		m := 0
		for i+m < len(line) && line[i+m] == '`' {
			m++
		}
		if m == n {
			return i + m
		}
		i += m
	}
	return -1
}

func runeBefore(s string, i int) rune {
	if i <= 0 {
		return '\n'
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r
}

func runeAt(s string, i int) rune {
	if i >= len(s) {
		return '\n'
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r
}

// isMdPunct is punctuation as CommonMark counts it for emphasis.
func isMdPunct(r rune) bool {
	if r < utf8.RuneSelf {
		return r < 0x7f && unicode.IsPrint(r) && !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ' '
	}
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// isWord is neither space nor punctuation.
func isWord(r rune) bool { return !unicode.IsSpace(r) && !isMdPunct(r) }
