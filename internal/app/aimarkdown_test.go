package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/markdown/parser"
)

// TestBoldAsMeant checks that bold CommonMark would leave as asterisks,
// next to Chinese punctuation, pairs once rewritten, and that the rest of
// the text, code and bold that already pairs, stays as written.
func TestBoldAsMeant(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"**结论：内存相对最紧。**以上是即时采样", "**结论：内存相对最紧**。以上是即时采样"},
		{"**注意：**先备份", "**注意**：先备份"},
		{"打开**「设置」**页面", "打开「**设置**」页面"},
		{"a **b** c\n**结论。**\n以上", "a **b** c\n**结论。**\n以上"},
		{"他说。**注意**：先备份", "他说。**注意**：先备份"},
		{"`**注意：**先`", "`**注意：**先`"},
		{"```\n**注意：**先\n```\n**注意：**先", "```\n**注意：**先\n```\n**注意**：先"},
		{"**注意：**先**备份", "**注意：**先**备份"},
		{"**。**后", "**。**后"},
	} {
		if got := boldAsMeant(tc.in); got != tc.want {
			t.Errorf("boldAsMeant(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// The rewritten text parses as bold.
	for _, src := range []string{"**结论：内存相对最紧。**以上是即时采样", "**注意：**先备份", "打开**「设置」**页面"} {
		if html := parser.Parse(boldAsMeant(src)).HTML(); !strings.Contains(html, "<strong>") {
			t.Errorf("%q: no bold in %q", src, html)
		}
	}
}
