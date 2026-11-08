package app

import (
	"strconv"

	"github.com/Licoy/sshyard/internal/ai/agent"
	"github.com/Licoy/sshyard/internal/ai/policy"
	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// toolCard keeps the operation, approval and execution status visible while
// command, output and other details stay folded until explicitly opened.
func (a *App) toolCard(c *ui.Context, cn *conn, it *aiItem, tok theme.Tokens, m locales.Messages) {
	card := it.tool
	ui.Column(c).FillWidth().Radius(theme.RadiusControl).Background(tok.Fill).Border(1, tok.Line).Clip().Children(func() {
		ui.Row(c.Key("toolhead")).Padding(theme.Space(1)).Gap(theme.Space(2)).AlignItems(ui.Center).FillWidth().Children(func() {
			ic, label := icons.Collapse, m.AiToolExpand()
			if it.open {
				ic, label = icons.Expand, m.AiToolCollapse()
			}
			if (IconButton{Icon: ic, Label: label, Active: it.open, Toggle: true, Size: Small}).Clicked(c, "details") {
				it.open = !it.open
			}
			ui.Icon(c, icons.Skill.SVG).Size(Small, Small).Shrink(0).TextColor(tok.TextMuted)
			ui.Text(c, toolName(m, card)).FontWeight(600).FontSize(theme.Footnote).SingleLine().Shrink(1).MinWidth(0)
			ui.Spacer(c)
			switch card.Verdict {
			case "auto", "full", "reviewer", "user":
				ui.Text(c, m.AiToolApproved(verdictText(m, card.Verdict))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(0)
			}
			status, tint := toolStatus(m, card, tok)
			ui.Text(c, status).FontSize(theme.Caption).FontWeight(500).TextColor(tint).SingleLine().Shrink(0)
		})
		if it.open {
			a.toolDetails(c.Key("details"), cn, card, tok, m)
		}
	})
}

func toolName(m locales.Messages, card agent.ToolCard) string {
	switch card.Name {
	case "run_command":
		return m.AiToolCommand()
	case "host_info":
		return m.AiToolHostInfo()
	case "pane_output":
		return m.AiToolPaneOutput()
	case "list_dir":
		return m.AiToolListDir()
	case "read_file":
		return m.AiToolReadFile()
	case "search_files":
		return m.AiToolSearchFiles()
	case "write_file":
		return m.AiToolWriteFile()
	case "terminal_send":
		return m.AiToolTerminalSend()
	case "terminal_key":
		return m.AiToolTerminalKey()
	}
	if card.Name != "" {
		return card.Name
	}
	return m.AiToolAction()
}

func toolStatus(m locales.Messages, card agent.ToolCard, tok theme.Tokens) (string, ui.Color) {
	switch card.Status {
	case "cancel":
		return m.AiVerdictCancel(), tok.TextMuted
	case "timeout":
		return m.AiVerdictTimeout(), tok.Warning
	}
	switch card.Verdict {
	case "cancel":
		return m.AiVerdictCancel(), tok.TextMuted
	case "timeout":
		return m.AiVerdictTimeout(), tok.Warning
	case "reject":
		return m.AiVerdictReject(), tok.Warning
	}
	if card.Phase != "done" {
		return m.AiToolRunning(), tok.Accent
	}
	if card.Exit == 0 {
		return m.AiToolSucceeded(), tok.Success
	}
	return m.AiToolFailed(), tok.Danger
}

func (a *App) toolDetails(c *ui.Context, cn *conn, card agent.ToolCard, tok theme.Tokens, m locales.Messages) {
	ui.Row(c).Padding(theme.Space(1), theme.Space(2)).Gap(theme.Space(2)).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, riskText(m, card.Risk)).FontSize(theme.Caption).TextColor(riskTint(tok, card.Risk))
		ui.Spacer(c)
		if card.Command != "" {
			a.codeActions(c, cn, card.Command, true, false, m)
		}
	})
	what := card.Command
	if what == "" {
		what = card.Path
	}
	if what != "" {
		toolDetailText(c, what, tok.Text, theme.Footnote)
	}
	if card.Dir != "" {
		toolDetailText(c, card.Dir, tok.TextMuted, theme.Caption)
	}
	if card.Review != "" {
		ui.Text(c.Key("review"), m.AiReviewNote()+": "+reviewNoteText(m, card.Review)).FontSize(theme.Caption).TextColor(tok.TextMuted).Padding(theme.Space(1), theme.Space(2))
	}
	if !card.Run && card.Refusal != "" {
		toolDetailText(c, toolOutput(m, card.Refusal), tok.TextMuted, theme.Caption)
	}
	if card.Diff != "" {
		toolDetailText(c.Key("diff"), card.Diff, tok.Text, theme.Caption)
	}
	if card.Output != "" && card.Phase == "done" {
		gutterScroll(c, ui.Scroll(c.Key("out")).MaxHeight(220).FillWidth()).Children(func() {
			toolDetailText(c, toolOutput(m, card.Output), tok.Text, theme.Caption)
		})
	}
	where := m.AiWhereRemote(a.pv(cn.host.Label()))
	if card.Where == string(policy.Local) {
		where = m.AiWhereLocal()
	}
	ui.Row(c.Key("toolfoot")).Padding(theme.Space(1), theme.Space(2)).Gap(theme.Space(2)).FillWidth().Children(func() {
		ui.Text(c, where).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
		if card.Phase == "done" && card.Run {
			ui.Spacer(c)
			// What is typed into a terminal has no exit code to show.
			if card.Exit >= 0 && card.Name != "terminal_send" && card.Name != "terminal_key" {
				tint := tok.TextMuted
				if card.Exit != 0 {
					tint = tok.Danger
				}
				ui.Text(c, m.AiExit(strconv.Itoa(card.Exit))).FontSize(theme.Caption).TextColor(tint).SingleLine()
			}
			ui.Text(c, m.AiDuration(strconv.FormatInt(card.DurMS, 10))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
		}
	})
}

func toolDetailText(c *ui.Context, text string, tint ui.Color, size float32) {
	ui.Box(c).FillWidth().Padding(theme.Space(1), theme.Space(2)).Selectable().Children(func() {
		ui.Text(c, text).Font("monospace").FontSize(size).TextColor(tint)
	})
}
