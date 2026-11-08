package policy

import "testing"

// TestDeviceGrades: what is typed into a device's command line is graded
// by the device's rules, abbreviations included, and "?" is graded as
// the line its Enter runs.
func TestDeviceGrades(t *testing.T) {
	eng := NewDeviceEngine()
	for _, tc := range []struct {
		line string
		want Risk
	}{
		{"show running-config", RiskReadOnly},
		{"sho ver", RiskReadOnly},
		{"show ip int brief | include up", RiskReadOnly},
		{"show run | begin interface | count", RiskReadOnly},
		{"do show ip route", RiskReadOnly},
		{"display current-configuration", RiskReadOnly},
		{"dis cur | inc vlan", RiskReadOnly},
		{"ping 10.0.0.1", RiskReadOnly},
		{"?", RiskReadOnly},
		{"show ?", RiskReadOnly},
		{"terminal length 0", RiskLow},
		{"screen-length 0 temporary", RiskLow},
		{"exit", RiskLow},
		{"undebug all", RiskLow},
		{"enable", RiskMedium},
		{"clear counters", RiskMedium},
		{"reset counters interface", RiskMedium},
		{"conf t", RiskHigh},
		{"system-view", RiskHigh},
		{"interface GigabitEthernet0/1", RiskHigh},
		{"shutdown", RiskHigh},
		{"undo shutdown", RiskHigh},
		{"no ip address", RiskHigh},
		{"wr", RiskHigh},
		{"write memory", RiskHigh},
		{"copy running-config startup-config", RiskHigh},
		{"save", RiskHigh},
		{"commit", RiskHigh},
		{"show run | redirect flash:run.txt", RiskHigh},
		{"y", RiskHigh},
		{"no", RiskHigh},
		{"reload", RiskCritical},
		{"reload ?", RiskCritical},
		{"rel?", RiskUnknown},
		{"reboot", RiskCritical},
		{"write erase", RiskCritical},
		{"wr er", RiskCritical},
		{"erase startup-config", RiskCritical},
		{"delete flash:c2960.bin", RiskCritical},
		{"format flash:", RiskCritical},
		{"reset saved-configuration", RiskCritical},
		{"request system reboot", RiskCritical},
		{"crypto key zeroize rsa", RiskCritical},
		{"sh run", RiskUnknown}, // "sh" is shutdown in an interface's configuration
		{"ip route 0.0.0.0 0.0.0.0 10.0.0.1", RiskUnknown},
		{"show run | tee flash:x", RiskHigh},
		{"show run |", RiskUnknown},
	} {
		g := eng.Classify(Action{Where: Remote, Tool: "terminal_send", Command: tc.line}, Scope{})
		if g.Risk != tc.want {
			t.Errorf("%q: %v (%s), want %v", tc.line, g.Risk, g.Why, tc.want)
		}
	}
}

// TestTerminalGrades: a line typed into a local shell is graded as the
// shell command it is, a key by what it may do, and a device engine
// grades the other tools as the built-in one does.
func TestTerminalGrades(t *testing.T) {
	eng := NewEngine()
	for _, tc := range []struct {
		tool, command string
		want          Risk
	}{
		{"terminal_send", "ls -la", RiskReadOnly},
		{"terminal_send", "rm -rf /", RiskCritical},
		{"terminal_send", "reload", RiskUnknown},
		{"terminal_key", "space", RiskReadOnly},
		{"terminal_key", "q", RiskReadOnly},
		{"terminal_key", "ctrl_c", RiskLow},
		{"terminal_key", "enter", RiskHigh},
		{"terminal_key", "f1", RiskUnknown},
	} {
		for _, e := range []Engine{eng, NewDeviceEngine()} {
			if tc.tool == "terminal_send" && e != eng {
				continue
			}
			g := e.Classify(Action{Where: Remote, Tool: tc.tool, Command: tc.command}, Scope{})
			if g.Risk != tc.want {
				t.Errorf("%s %q: %v (%s), want %v", tc.tool, tc.command, g.Risk, g.Why, tc.want)
			}
		}
	}
	if g := NewDeviceEngine().Classify(Action{Where: Remote, Tool: "pane_output"}, Scope{}); g.Risk != RiskReadOnly {
		t.Fatalf("pane_output on a device: %v", g.Risk)
	}
	// The mode table is the built-in one: full allows what is critical.
	d := NewDeviceEngine().Decide(ModeFull, Grade{Risk: RiskCritical}, false)
	if d.Outcome != Allow || !d.Red {
		t.Fatalf("full on a device: %+v", d)
	}
	if d := NewDeviceEngine().Decide(ModeAsk, Grade{Risk: RiskHigh}, false); d.Outcome != Ask {
		t.Fatalf("ask on a device: %+v", d)
	}
}
