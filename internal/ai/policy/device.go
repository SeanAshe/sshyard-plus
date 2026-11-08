package policy

import "strings"

// NewDeviceEngine grades what an agent types into a network device's own
// command line, over Telnet or a serial port: Cisco, Huawei, H3C, Juniper
// and their like. Every other action is graded as NewEngine grades it.
// The device's prompt mode is not tracked: what the rules do not know is
// Unknown, which asks wherever High would.
func NewDeviceEngine() Engine { return deviceEngine{} }

type deviceEngine struct{ engine }

func (d deviceEngine) Classify(a Action, scope Scope) Grade {
	if a.Where == Remote && strings.EqualFold(strings.TrimSpace(a.Tool), "terminal_send") {
		return gradeDevice(a.Command).Grade()
	}
	return d.engine.Classify(a, scope)
}

// abbrev reports whether tok is word or an abbreviation of it at least
// least characters long, as device command lines take them.
func abbrev(tok, word string, least int) bool {
	return len(tok) >= least && strings.HasPrefix(word, tok)
}

// deviceFilters are what may follow "|" without changing anything: they
// narrow or page what a show command prints.
var deviceFilters = []struct {
	word  string
	least int
}{
	{"include", 1}, {"exclude", 1}, {"begin", 1}, {"section", 1}, {"count", 1},
	{"grep", 1}, {"match", 1}, {"except", 1}, {"find", 1}, {"no-more", 2},
	{"display", 1}, {"last", 1}, {"trim", 1}, {"linenum", 2}, {"format", 1},
}

func gradeDevice(cmd string) partial {
	line := strings.ToLower(strings.TrimSpace(cmd))
	if line == "" {
		return unk("empty line")
	}
	main, filters, piped := strings.Cut(line, "|")
	g := gradeDeviceCommand(strings.Fields(main))
	if !piped {
		return g
	}
	for _, f := range strings.Split(filters, "|") {
		fs := strings.Fields(f)
		if len(fs) == 0 {
			return better(g, unk("empty filter"))
		}
		known := false
		for _, df := range deviceFilters {
			if abbrev(fs[0], df.word, df.least) {
				known = true
				break
			}
		}
		if !known {
			// redirect, tee, append and save write what is shown to a file.
			g = better(g, partial{cat: "write", risk: RiskHigh, prefix: fs[0], why: "output filter writes: " + fs[0]})
		}
	}
	return g
}

func gradeDeviceCommand(words []string) partial {
	if len(words) == 0 {
		return unk("empty command")
	}
	// "?" shows help and leaves the line before it to be run by the Enter
	// that follows: "reload ?" reloads. It is graded as what it leaves.
	var f []string
	for _, w := range words {
		if w = strings.TrimRight(w, "?"); w != "" {
			f = append(f, w)
		}
	}
	if len(f) == 0 {
		return partial{cat: "read", risk: RiskReadOnly, prefix: "?", why: "device help"}
	}
	// Cisco's "do" runs an exec command from configuration mode.
	if f[0] == "do" && len(f) > 1 {
		f = f[1:]
	}
	verb := f[0]
	arg := ""
	if len(f) > 1 {
		arg = f[1]
	}
	read := func() partial {
		return partial{cat: "read", risk: RiskReadOnly, prefix: f[0], why: "device read: " + f[0]}
	}
	low := func(why string) partial { return partial{cat: "low-write", risk: RiskLow, prefix: f[0], why: why} }
	medium := func(cat Category, why string) partial {
		return partial{cat: cat, risk: RiskMedium, prefix: f[0], why: why}
	}
	high := func(cat Category, why string) partial {
		return partial{cat: cat, risk: RiskHigh, prefix: f[0], why: why}
	}
	for _, t := range f {
		if strings.Contains(t, "zeroize") || t == "factory-reset" || t == "factory-default" {
			return crit(f[0], "device factory reset: "+t)
		}
	}
	switch {
	// "sh" is not show: in an interface's configuration it is shutdown.
	case abbrev(verb, "show", 3), abbrev(verb, "display", 3), verb == "ping", abbrev(verb, "traceroute", 5),
		abbrev(verb, "tracert", 5), verb == "dir", verb == "pwd", verb == "more", verb == "who", verb == "uptime":
		return read()
	case abbrev(verb, "terminal", 4), abbrev(verb, "screen-length", 8), abbrev(verb, "screen-width", 8),
		verb == "exit", verb == "end", verb == "quit", verb == "return", verb == "cd":
		return low("device session: " + verb)
	case verb == "undebug" || verb == "no" && abbrev(arg, "debug", 3):
		return low("device debug off")
	case abbrev(verb, "enable", 2), verb == "super", abbrev(verb, "disable", 4):
		return medium("permission", "device privilege: "+verb)
	case abbrev(verb, "reload", 4), abbrev(verb, "reboot", 3):
		return crit(verb, "device restart")
	case abbrev(verb, "erase", 3), abbrev(verb, "format", 4), abbrev(verb, "delete", 3):
		return crit(verb, "device storage erase: "+verb)
	case abbrev(verb, "write", 2) && abbrev(arg, "erase", 2):
		return crit(verb, "device configuration erase")
	case abbrev(verb, "reset", 3) && (abbrev(arg, "saved-configuration", 5) || abbrev(arg, "factory", 3)):
		return crit(verb, "device configuration erase")
	case verb == "request" && arg == "system" && len(f) > 2 &&
		(abbrev(f[2], "reboot", 3) || abbrev(f[2], "halt", 2) || abbrev(f[2], "power-off", 5) || abbrev(f[2], "zeroize", 3)):
		return crit(verb, "device restart")
	case abbrev(verb, "write", 2), abbrev(verb, "copy", 2), abbrev(verb, "save", 2), verb == "commit",
		abbrev(verb, "startup", 5), verb == "boot", abbrev(verb, "rename", 3), verb == "move", verb == "rmdir", verb == "mkdir":
		return high("write", "device configuration or file write: "+verb)
	case abbrev(verb, "configure", 4), abbrev(verb, "system-view", 3), verb == "edit", verb == "set", verb == "no",
		verb == "undo", abbrev(verb, "interface", 3), abbrev(verb, "rollback", 5), abbrev(verb, "shutdown", 4):
		return high("write", "device configuration: "+verb)
	case verb == "request", verb == "license", abbrev(verb, "debug", 3), abbrev(verb, "install", 4):
		return high("service", "device operation: "+verb)
	case abbrev(verb, "clear", 3), abbrev(verb, "reset", 3), verb == "test":
		return medium("service", "device state reset: "+verb)
	case answer(line(f)):
		return high("write", "answers a device prompt")
	}
	return unk("unlisted device command: " + verb)
}

func line(f []string) string { return strings.Join(f, " ") }

// answer reports an answer to a confirmation, which takes the risk of
// what it confirms: a [confirm] after reload restarts the device.
func answer(s string) bool {
	switch s {
	case "y", "yes", "n", "no", "c", "a", "all":
		return true
	}
	return false
}

// gradeKey grades a key pressed in a terminal. Enter may confirm what a
// prompt asks; space and q turn or leave a pager; Ctrl+C interrupts.
func gradeKey(key string) partial {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "space", "q":
		return partial{cat: "read", risk: RiskReadOnly, prefix: key, why: "pager key"}
	case "ctrl_c":
		return partial{cat: "low-write", risk: RiskLow, prefix: key, why: "interrupt"}
	case "enter":
		return partial{cat: "write", risk: RiskHigh, prefix: key, why: "Enter may confirm a prompt"}
	}
	return unk("unknown key")
}
