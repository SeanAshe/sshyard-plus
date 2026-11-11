package app

import "strings"

// dropText is what files dropped on a local terminal type at its prompt:
// each path quoted for the shell (session.Shell's ID) on goos, separated
// and followed by a space, never by a newline, so nothing runs until the
// user presses Enter. A path with a control character is left out, as no
// quoting keeps it from ending or breaking the line; "" when none is left.
func dropText(paths []string, goos, shell string) string {
	var b strings.Builder
	for _, p := range paths {
		if p == "" || hasControl(p) {
			continue
		}
		var q string
		switch {
		case goos != "windows":
			q = quotePOSIX(p)
		case shell == "gitbash":
			q = quotePOSIX(windowsToPOSIX(p, "/"))
		case strings.HasPrefix(shell, "wsl:"):
			q = quotePOSIX(wslPath(p, strings.TrimPrefix(shell, "wsl:")))
		case shell == "pwsh" || shell == "powershell":
			q = quotePowerShell(p)
		default: // cmd
			if strings.Contains(p, `"`) {
				continue // not a Windows file name, and cmd cannot quote it
			}
			q = quoteCmd(p)
		}
		b.WriteString(q)
		b.WriteByte(' ')
	}
	return b.String()
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// quotePOSIX escapes with a backslash every ASCII character but letters,
// digits and -_./,:@+, as macOS Terminal does: it holds in sh, bash, zsh
// and fish alike (a backslash before a letter would not, in fish).
// Other bytes, of UTF-8 names, go as they are.
func quotePOSIX(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		safe := c >= 0x80 || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("-_./,:@+", c) >= 0
		if !safe {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return b.String()
}

// quotePowerShell wraps p in double quotes when PowerShell would read any
// of it otherwise, with ` before what is still special inside them: $,
// the backtick itself and the typographic double quotes it takes as ".
func quotePowerShell(p string) string {
	if !strings.ContainsAny(p, " \t&(){}[];,'`$@#|<>\u2018\u2019\u201a\u201b\u201c\u201d\u201e") {
		return p
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range p {
		if strings.ContainsRune("`$\u201c\u201d\u201e", r) {
			b.WriteByte('`')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// quoteCmd wraps p in double quotes when it has a space or a character
// cmd.exe gives a meaning (its own list, "for /?").
func quoteCmd(p string) string {
	if !strings.ContainsAny(p, " \t&()[]{}^=;!'+,`~|<>") {
		return p
	}
	return `"` + p + `"`
}

// windowsToPOSIX writes a Windows path with slashes and its drive as a
// folder under root: C:\a\b is /c/a/b under "/", /mnt/c/a/b under "/mnt/".
func windowsToPOSIX(p, root string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) < 2 || p[1] != ':' {
		return p
	}
	if d := p[0] | 0x20; d >= 'a' && d <= 'z' {
		rest := p[2:]
		if rest != "" && rest[0] != '/' {
			rest = "/" + rest
		}
		return root + string(rune(d)) + rest
	}
	return p
}

// wslPath is where WSL's distro sees the Windows path p: drives under
// /mnt, and the distro's own files (\\wsl$\distro\..., or
// \\wsl.localhost\distro\...) at their own path.
func wslPath(p, distro string) string {
	for _, pre := range []string{`\\wsl$\`, `\\wsl.localhost\`} {
		if len(p) > len(pre) && strings.EqualFold(p[:len(pre)], pre) {
			rest := p[len(pre):]
			name, after, _ := strings.Cut(rest, `\`)
			if strings.EqualFold(name, distro) {
				return "/" + strings.ReplaceAll(after, `\`, "/")
			}
		}
	}
	return windowsToPOSIX(p, "/mnt/")
}
