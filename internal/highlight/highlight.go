// Package highlight colors a terminal's output: a plain prompt and
// command line, words such as errors and warnings, addresses, links and
// times, by built-in rules (Presets) and the user's own (Rule). It
// colors what the screen shows, never what the program wrote, and leaves
// text the program colored as it is unless asked otherwise.
package highlight

import (
	"bytes"
	"errors"
	"regexp"
	"slices"
	"unicode"
	"unicode/utf8"

	"github.com/Licoy/sshyard/internal/vt"
)

// Style is how a match shows: Color is an index of the terminal's 16
// colors, for the text or, with Background, behind it.
type Style struct {
	Color      uint8 `json:"color"`
	Background bool  `json:"background,omitempty"`
	Bold       bool  `json:"bold,omitempty"`
	Underline  bool  `json:"underline,omitempty"`
}

// Rule is a rule of the user's: text, or a regular expression (RE2), and
// how its matches show.
type Rule struct {
	Pattern       string `json:"pattern"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"caseSensitive,omitempty"`
	WholeWord     bool   `json:"wholeWord,omitempty"`
	Style
	Off bool `json:"off,omitempty"`
}

// MaxRules bounds the user's rules, and MaxPattern their patterns' bytes.
const (
	MaxRules   = 200
	MaxPattern = 1000
)

// ErrEmpty is the error of a rule without a pattern.
var ErrEmpty = errors.New("highlight: empty pattern")

// Compile is the expression of r's pattern.
func (r Rule) Compile() (*regexp.Regexp, error) {
	if r.Pattern == "" {
		return nil, ErrEmpty
	}
	expr := r.Pattern
	if !r.Regex {
		expr = regexp.QuoteMeta(expr)
		// \b only where the pattern starts or ends with a word's letter:
		// it never matches beside the letters of CJK text.
		if r.WholeWord {
			if c, _ := utf8.DecodeRuneInString(r.Pattern); wordRune(c) {
				expr = `\b` + expr
			}
			if c, _ := utf8.DecodeLastRuneInString(r.Pattern); wordRune(c) {
				expr += `\b`
			}
		}
	} else {
		// The pattern's own errors, not those of what wraps it.
		if _, err := regexp.Compile(expr); err != nil {
			return nil, err
		}
		if r.WholeWord {
			expr = `\b(?:` + expr + `)\b`
		}
	}
	if !r.CaseSensitive {
		expr = "(?i)" + expr
	}
	return regexp.Compile(expr)
}

// wordRune says whether \b sees c as a word's.
func wordRune(c rune) bool {
	return c < utf8.RuneSelf && (c == '_' || unicode.IsLetter(c) || unicode.IsDigit(c))
}

// rule is a rule ready to match, by an expression or by the words of
// the text. An expression's matches (or, with groups, the first of its
// groups that took part) show in the styles: styles[0], or styles[i-1]
// for group i. It runs only on a text with one of need in it, in lower
// case, if need has any. A word rule matches the words of the text (runs
// of ASCII letters, digits and _, as \b sees them) that word accepts:
// lower is the word in lower case.
type rule struct {
	re     *regexp.Regexp
	groups bool
	need   []string
	word   func(w, lower []byte) bool
	styles []Style
}

// Span is a match in a text: the bytes From to To show in Style.
type Span struct {
	From, To int
	Style    Style
}

// Highlighter matches the rules of the user's, then the presets on, in
// the lines a terminal shows. It remembers the matches of the lines it
// has seen, so a screen that scrolls costs only its new lines. It is not
// safe for concurrent use.
type Highlighter struct {
	rules []rule
	// styles are the rules' styles one after the other: rule i's start at
	// base[i].
	styles []Style
	base   []int
	seen   map[string][]Span
	// buffers the lines share
	text  []byte
	cells []cellRef
	owner []int16
	lower []byte
	words [][2]int
}

// cellRef is the cell a byte of a text is in.
type cellRef struct{ row, col int32 }

// maxSeen bounds the lines a Highlighter remembers.
const maxSeen = 4096

// New is a Highlighter of the user's rules on (those that compile), first,
// and the presets whose IDs on lists.
func New(rules []Rule, on []string) *Highlighter {
	h := &Highlighter{}
	for _, r := range rules {
		if r.Off {
			continue
		}
		if re, err := r.Compile(); err == nil {
			h.add(rule{re: re, styles: []Style{r.Style}})
		}
	}
	for _, early := range []bool{true, false} {
		for _, p := range Presets {
			if p.early == early && slices.Contains(on, p.ID) {
				for _, r := range p.rules {
					h.add(r)
				}
			}
		}
	}
	return h
}

func (h *Highlighter) add(r rule) {
	h.rules = append(h.rules, r)
	h.base = append(h.base, len(h.styles))
	h.styles = append(h.styles, r.styles...)
}

// Empty says whether h has no rules.
func (h *Highlighter) Empty() bool { return h == nil || len(h.rules) == 0 }

// Spans are the matches in text, in order and apart. Where matches
// overlap, the earlier rule's wins, and a later match that overlaps one
// shown does not show at all, so no word is colored in part.
func (h *Highlighter) Spans(text string) []Span {
	return h.spans([]byte(text))
}

func (h *Highlighter) spans(text []byte) []Span {
	if s, ok := h.seen[string(text)]; ok {
		return s
	}
	var out []Span
	if len(h.rules) > 0 && len(bytes.TrimSpace(text)) > 0 {
		h.match(text)
		owner := h.owner
		for b := 0; b < len(owner); {
			if owner[b] < 0 {
				b++
				continue
			}
			e := b
			for e < len(owner) && owner[e] == owner[b] {
				e++
			}
			out = append(out, Span{b, e, h.styles[owner[b]]})
			b = e
		}
	}
	if h.seen == nil || len(h.seen) >= maxSeen {
		h.seen = make(map[string][]Span)
	}
	h.seen[string(text)] = out
	return out
}

// match sets owner, for each byte of text, to the style of the match it
// is in, -1 for none.
func (h *Highlighter) match(text []byte) {
	owner := h.owner[:0]
	lower := h.lower[:0]
	for _, c := range text {
		owner = append(owner, -1)
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		lower = append(lower, c)
	}
	h.owner, h.lower = owner, lower
	h.words = h.words[:0]
	tokenized := false
	claim := func(from, to int, style int) {
		if from < 0 || from == to || taken(owner[from:to]) {
			return
		}
		for b := from; b < to; b++ {
			owner[b] = int16(style)
		}
	}
	for i, r := range h.rules {
		base := h.base[i]
		if r.word != nil {
			if !tokenized {
				h.tokenize(text)
				tokenized = true
			}
			for _, w := range h.words {
				if r.word(text[w[0]:w[1]], lower[w[0]:w[1]]) {
					claim(w[0], w[1], base)
				}
			}
			continue
		}
		if len(r.need) > 0 && !slices.ContainsFunc(r.need, func(n string) bool { return bytes.Contains(lower, []byte(n)) }) {
			continue
		}
		for _, m := range r.re.FindAllSubmatchIndex(text, -1) {
			if !r.groups {
				claim(m[0], m[1], base)
				continue
			}
			for g := 1; 2*g < len(m) && g <= len(r.styles); g++ {
				if m[2*g] >= 0 {
					claim(m[2*g], m[2*g+1], base+g-1)
					break
				}
			}
		}
	}
}

// tokenize finds the words of text.
func (h *Highlighter) tokenize(text []byte) {
	for b := 0; b < len(text); {
		if !wordByte(text[b]) {
			b++
			continue
		}
		e := b
		for e < len(text) && wordByte(text[e]) {
			e++
		}
		h.words = append(h.words, [2]int{b, e})
		b = e
	}
}

func wordByte(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func taken(owner []int16) bool {
	for _, o := range owner {
		if o >= 0 {
			return true
		}
	}
	return false
}

// Apply shows the matches in the cells of lines, the rows a terminal
// shows, top first. A line that wraps into the next is matched with it.
// The cells the program colored keep their colors unless override.
func (h *Highlighter) Apply(lines []vt.Line, override bool) {
	if h.Empty() {
		return
	}
	for y := 0; y < len(lines); {
		// The rows of one line of the output.
		e := y
		for e < len(lines)-1 && lines[e].Wrapped {
			e++
		}
		text, cells := h.text[:0], h.cells[:0]
		for row := y; row <= e; row++ {
			for x, c := range lines[row].Cells {
				if c.Width == 0 {
					continue
				}
				r := c.Rune
				if r == 0 {
					r = ' '
				}
				n := len(text)
				text = utf8.AppendRune(text, r)
				if c.Comb != 0 {
					text = append(text, c.Comb.String()...)
				}
				for range len(text) - n {
					cells = append(cells, cellRef{int32(row), int32(x)})
				}
			}
		}
		h.text, h.cells = text, cells
		for _, s := range h.spans(text) {
			for b := s.From; b < s.To; b++ {
				// The bytes of a rune share its cell: style it once.
				if b > s.From && cells[b] == cells[b-1] {
					continue
				}
				ref := cells[b]
				style(&lines[ref.row].Cells[ref.col], s.Style, override)
			}
		}
		y = e + 1
	}
}

// style shows c in s, if the program left it its default colors or
// override.
func style(c *vt.Cell, s Style, override bool) {
	if !override && (!c.FG.IsDefault() || !c.BG.IsDefault() || c.Attr&vt.Inverse != 0) {
		return
	}
	if s.Background {
		c.BG = vt.Indexed(s.Color)
	} else {
		c.FG = vt.Indexed(s.Color)
	}
	if s.Bold {
		c.Attr |= vt.Bold
	}
	if s.Underline {
		c.Attr |= vt.Underline
	}
}
