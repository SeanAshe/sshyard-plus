package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The services panel: the systemd services of the focused session's
// server, the administrator's own first. It is read when the panel shows
// and when the user asks, not on a timer: a reading starts several
// systemctl commands, and services change seldom. A service can be
// started, stopped, restarted, enabled at boot, and opened for its state
// and journal.

const (
	svcListTimeout = 45 * time.Second
	svcDoTimeout   = time.Minute
)

// svcState is what the services panel read, of which session, and the
// service whose details are open.
type svcState struct {
	conn, connects, gen int
	running             bool
	info                session.ServiceInfo
	read                bool
	err                 error
	query               string
	filter              int
	list                ui.ListState
	busy                string
	detailOpen          bool
	detailName          string
	detailTab           int
	detail              session.ServiceStatus
	detailOK            bool
	detailBusy          bool
	detailErr           string
	journal             string
	journalBusy         bool
	journalErr          string
	detailGen           int
}

type svcLine struct {
	group  bool
	custom bool
	count  int
	unit   session.Unit
}

func svcErrText(m locales.Messages, err error) string {
	switch {
	case errors.Is(err, session.ErrServiceRoot):
		return m.SvcNeedsRoot()
	case errors.Is(err, session.ErrServiceNoSudo):
		return m.SvcNeedsRootNoSudo()
	}
	return m.SvcError(err.Error())
}

func (a *App) readServices(c *ui.Context, tc *conn, now bool) {
	s := a.services
	connects := tc.s.Status().Connects
	if s.conn != tc.id || s.connects != connects {
		s.gen++
		s.conn, s.connects, s.running = tc.id, connects, false
		s.info, s.read, s.err, s.busy = session.ServiceInfo{}, false, nil, ""
		s.detailOpen = false
	}
	if s.running || (s.read && !now) {
		return
	}
	client := tc.s.Client()
	if client == nil {
		return
	}
	s.running = true
	gen := s.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), svcListTimeout)
		info, err := session.ServicesList(ctx, client)
		cancel()
		a.post(func() {
			if s.gen != gen {
				return
			}
			s.running = false
			s.read, s.err = true, err
			if err == nil {
				s.info = info
			}
		})
	}()
}

func (a *App) serviceDo(tc *conn, action session.ServiceAction, name string) {
	s := a.services
	client := tc.s.Client()
	if client == nil || s.busy != "" || !session.ServiceNameOK(name) {
		return
	}
	s.busy = name
	gen := s.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), svcDoTimeout)
		err := session.ServiceDo(ctx, client, action, name)
		cancel()
		a.post(func() {
			if s.gen != gen {
				return
			}
			s.busy = ""
			s.read = false
			if err != nil {
				a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
					return ui.Toast{Title: svcErrText(m, err), Type: "error"}
				})
			}
			if s.detailOpen && s.detailName == name {
				a.loadService(tc, name)
			}
		})
	}()
}

func (a *App) openService(tc *conn, name string) {
	s := a.services
	s.detailOpen, s.detailName, s.detailTab = true, name, 0
	a.loadService(tc, name)
}

func (a *App) loadService(tc *conn, name string) {
	s := a.services
	client := tc.s.Client()
	if client == nil || !session.ServiceNameOK(name) {
		return
	}
	s.detailGen++
	s.detail, s.detailOK, s.detailBusy, s.detailErr = session.ServiceStatus{}, false, true, ""
	s.journal, s.journalBusy, s.journalErr = "", true, ""
	gen, dgen := s.gen, s.detailGen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), svcDoTimeout)
		st, err := session.ServiceStatusOf(ctx, client, name)
		cancel()
		a.post(func() {
			if s.gen != gen || s.detailGen != dgen || s.detailName != name {
				return
			}
			s.detailBusy = false
			if err != nil {
				s.detailErr = err.Error()
				return
			}
			s.detail, s.detailOK = st, true
		})
	}()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), svcDoTimeout)
		out, err := session.ServiceLogs(ctx, client, name)
		cancel()
		a.post(func() {
			if s.gen != gen || s.detailGen != dgen || s.detailName != name {
				return
			}
			s.journalBusy, s.journal = false, out
			if err != nil {
				s.journalErr = err.Error()
			}
		})
	}()
}

func (a *App) servicesPanel(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	if a.services == nil {
		a.services = &svcState{list: ui.ListState{RoundRows: true}}
	}
	s := a.services
	cn := a.focused()
	tc := a.sshConn(cn)
	if note := a.usageNote(m, cn, tc); note != "" {
		panelNote(c, tok, note)
		return
	}
	a.readServices(c, tc, false)
	switch {
	case s.err != nil && !s.read:
		panelNote(c, tok, svcErrText(m, s.err))
		return
	case !s.read:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			panelNote(c, tok, m.MonitorWaiting())
		})
		return
	case !s.info.Ready:
		if s.info.System != "" && s.info.System != "Linux" {
			panelNote(c, tok, m.SvcUnsupported(s.info.System))
		} else {
			panelNote(c, tok, m.SvcNoSystemd())
		}
		return
	}
	all, run, stop, failed := session.CountUnits(s.info.Units, s.query)
	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		summary := svcSummary(m, s.info)
		col := tok.TextMuted
		if s.info.Degraded() {
			col = tok.Warning
		}
		ui.Text(c, summary).FontSize(theme.Caption).TextColor(col).SingleLine().Grow(1).Shrink(1)
		if s.running {
			ui.Spinner(c)
		}
		if (IconButton{Icon: icons.Refresh, Label: m.Refresh(), Disabled: s.running}).Clicked(c, "svcrefresh") {
			s.err = nil
			a.readServices(c, tc, true)
		}
	})
	in := ui.TextInputBase(c.Key("q"), &s.query).Placeholder(m.SvcSearch()).Label(m.SvcSearch()).FillWidth().Radius(theme.Capsule)
	if in.Changed() {
		s.list.ScrollTo(0, ui.Start)
	}
	labels := []string{m.SvcAll(), m.SvcRunning(), m.SvcStopped(), m.SvcFailed()}
	counts := []int{all, run, stop, failed}
	seg := segmentedControl(c.Key("svcfilt"), &s.filter, false, labels, theme.Space(2), func(i int, col ui.Color) {
		ui.Text(c, labels[i]).FontSize(theme.Caption).FontWeight(500).TextColor(col).SingleLine()
		ui.Text(c, strconv.Itoa(counts[i])).FontSize(theme.Caption).TextColor(col).SingleLine()
	}).FillWidth()
	if seg.Changed() {
		s.list.ScrollTo(0, ui.Start)
	}
	if s.err != nil {
		ui.Text(c, svcErrText(m, s.err)).FontSize(theme.Caption).TextColor(tok.Danger)
	}
	lines := svcLines(s.info, session.SvcFilter(s.filter), s.query)
	if len(lines) == 0 {
		if strings.TrimSpace(s.query) == "" && s.filter == int(session.SvcAll) {
			panelNote(c, tok, m.SvcEmpty())
		} else {
			panelNote(c, tok, m.SvcNoMatch())
		}
		a.serviceDetail(c, m, tok, tc)
		return
	}
	s.list.Header = func(i int) bool { return i >= 0 && i < len(lines) && lines[i].group }
	s.list.Key = func(i int) any {
		if i < 0 || i >= len(lines) {
			return i
		}
		if lines[i].group {
			if lines[i].custom {
				return "custom"
			}
			return "system"
		}
		return lines[i].unit.Name
	}
	bg := a.backgroundContentFill(tok.Canvas)
	ui.List(c.Key("svcs"), &s.list, len(lines), func(i int) {
		line := lines[i]
		if line.group {
			label := m.SvcSystem()
			if line.custom {
				label = m.SvcCustom()
			}
			ui.Row(c).FillWidth().Background(bg).Padding(8, 2, 2, 2).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).FontSize(theme.Footnote).FontWeight(600).SingleLine()
				ui.Text(c, m.SvcGroupCount(strconv.Itoa(line.count))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			})
			return
		}
		a.svcCard(c, m, tok, tc, line.unit)
	}).Gap(8).Grow(1).Shrink(1).MinHeight(0).FillWidth()
	a.serviceDetail(c, m, tok, tc)
}

func svcLines(info session.ServiceInfo, filter session.SvcFilter, query string) []svcLine {
	custom, system := session.FilterUnits(info.Units, filter, query)
	var lines []svcLine
	add := func(own bool, units []session.Unit) {
		if len(units) == 0 {
			return
		}
		lines = append(lines, svcLine{group: true, custom: own, count: len(units)})
		for _, u := range units {
			lines = append(lines, svcLine{unit: u})
		}
	}
	add(true, custom)
	add(false, system)
	return lines
}

func svcSummary(m locales.Messages, info session.ServiceInfo) string {
	var parts []string
	if info.Version != "" {
		parts = append(parts, info.Version)
	}
	if info.State != "" {
		parts = append(parts, svcSystemLabel(m, info.State))
	}
	return strings.Join(parts, " · ")
}

func (a *App) svcCard(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn, u session.Unit) {
	s := a.services
	tint := svcTint(tok, u.Active)
	ui.Column(c).FillWidth().Padding(8, 10).Gap(6).Radius(theme.RadiusControl).Background(tok.Inset).Border(1, tok.Line).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			info := ui.Column(c).Gap(2).Grow(1).Shrink(1).MinWidth(0).Cursor(ui.CursorPointer)
			info.Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).FillWidth().Children(func() {
					dot(c, tint)
					ui.Text(c, u.Name).FontSize(theme.Footnote).FontWeight(600).SingleLine().Grow(1).Shrink(1)
				})
				desc := u.Description
				if desc == "" {
					desc = m.SvcNoDescription()
				}
				ui.Text(c, desc).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					svcTag(c, svcActiveLabel(m, u.Active), tint)
					if label, ok := svcFileLabel(m, u.FileState); ok {
						col := tok.TextMuted
						if strings.HasPrefix(u.FileState, "enabled") {
							col = tok.Success
						}
						svcTag(c, label, col)
					}
				})
			})
			b := info.Bounds()
			info.HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind != ui.InputPointerDown {
					return false
				}
				if ev.Button == 1 {
					unit := u
					a.openContext(b.X+ev.X, b.Y+ev.Y, func() []menuEntry { return a.svcMenu(m, tc, unit) })
					return true
				}
				if ev.Button == 0 {
					a.openService(tc, u.Name)
				}
				return false
			})
			ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
				for _, act := range u.Actions() {
					act := act
					if (IconButton{Icon: svcActionIcon(act), Label: svcActionLabel(m, act), Danger: act == session.ServiceStop, Disabled: s.busy != "", Size: Small}).Clicked(c, string(act)+":"+u.Name) {
						a.serviceDo(tc, act, u.Name)
					}
				}
				if (IconButton{Icon: icons.More, Label: m.SvcDetails(), Size: Small}).Clicked(c, "more:"+u.Name) {
					a.openService(tc, u.Name)
				}
			})
		})
	})
}

func svcTag(c *ui.Context, label string, col ui.Color) {
	ui.Box(c).Padding(1, 6).Radius(theme.Capsule).Background(col.Alpha(0.12)).Children(func() {
		ui.Text(c, label).FontSize(theme.Caption).TextColor(col).SingleLine()
	})
}

func (a *App) svcMenu(m locales.Messages, tc *conn, u session.Unit) []menuEntry {
	s := a.services
	off := s != nil && s.busy != ""
	entries := []menuEntry{{Icon: icons.Info, Label: m.SvcDetails(), Do: func() { a.openService(tc, u.Name) }}, menuSep}
	for _, act := range u.Actions() {
		act := act
		entries = append(entries, menuEntry{Icon: svcActionIcon(act), Label: svcActionLabel(m, act), Danger: act == session.ServiceStop, Disabled: off, Do: func() {
			a.serviceDo(tc, act, u.Name)
		}})
	}
	if boot, ok := u.BootAction(); ok {
		boot := boot
		entries = append(entries, menuSep, menuEntry{Icon: svcActionIcon(boot), Label: svcActionLabel(m, boot), Disabled: off, Do: func() {
			a.serviceDo(tc, boot, u.Name)
		}})
	}
	return entries
}

func svcActionIcon(act session.ServiceAction) *icons.Icon {
	switch act {
	case session.ServiceStop:
		return icons.Halt
	case session.ServiceRestart:
		return icons.Reconnect
	case session.ServiceEnable:
		return icons.Check
	case session.ServiceDisable:
		return icons.Close
	}
	return icons.Resume
}

func svcActionLabel(m locales.Messages, act session.ServiceAction) string {
	switch act {
	case session.ServiceStop:
		return m.SvcStop()
	case session.ServiceRestart:
		return m.SvcRestart()
	case session.ServiceEnable:
		return m.SvcEnable()
	case session.ServiceDisable:
		return m.SvcDisable()
	}
	return m.SvcStart()
}

func svcTint(tok theme.Tokens, active string) ui.Color {
	switch active {
	case "active", "reloading":
		return tok.Success
	case "failed":
		return tok.Danger
	case "activating", "deactivating":
		return tok.Warning
	}
	return tok.TextMuted
}

func (a *App) serviceDetail(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	s := a.services
	if s == nil || !s.detailOpen {
		return
	}
	unit := session.Unit{Name: s.detailName}
	for _, u := range s.info.Units {
		if u.Name == s.detailName {
			unit = u
			break
		}
	}
	if s.detailOK {
		unit.Active, unit.FileState = s.detail.Active, s.detail.FileState
		if unit.Description == "" {
			unit.Description = s.detail.Description
		}
	}
	sheet(ui.Modal(c, &s.detailOpen, func() {
		ui.Column(c).Gap(12).Width(560).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
				tile(c, icons.Services, theme.Brand, 36)
				ui.Column(c).Gap(2).Grow(1).Shrink(1).MinWidth(0).Children(func() {
					sheetTitle(c, s.detailName)
					desc := unit.Description
					if desc == "" {
						desc = m.SvcNoDescription()
					}
					ui.Text(c, desc).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
				})
				if s.detailBusy || s.journalBusy {
					ui.Spinner(c)
				}
				if (IconButton{Icon: icons.Refresh, Label: m.Refresh(), Disabled: s.detailBusy && s.journalBusy}).Clicked(c, "svcdetailrefresh") {
					a.loadService(tc, s.detailName)
				}
			})
			formSegmented(c.Key("svctab"), &s.detailTab, false, m.SvcDetails(), m.SvcJournal()).Label(s.detailName)
			if s.detailTab == 0 {
				a.serviceStatus(c, m, tok, tc, unit)
			} else {
				a.serviceJournal(c, m, tok)
			}
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Close(), true, false, false) {
					s.detailOpen = false
				}
			})
		})
	}), tok)
}

func (a *App) serviceStatus(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn, unit session.Unit) {
	s := a.services
	if s.detailErr != "" {
		panelNote(c, tok, m.SvcUnreadable())
		ui.Text(c, s.detailErr).FontSize(theme.Caption).TextColor(tok.Danger)
		return
	}
	if !s.detailOK {
		return
	}
	st := s.detail
	dash := m.MetricsDash()
	active := svcActiveLabel(m, st.Active)
	if sub := svcSubLabel(m, st.Sub); sub != "" && sub != active {
		active += " · " + sub
	}
	boot := dash
	if label, ok := svcFileLabel(m, st.FileState); ok {
		boot = label
	}
	pid := dash
	if st.MainPID != 0 {
		pid = strconv.FormatUint(uint64(st.MainPID), 10)
	}
	mem, tasks, restarts, exit := dash, dash, dash, dash
	if st.Memory != nil {
		mem = fmtBytes(*st.Memory)
	}
	if st.Tasks != nil {
		tasks = strconv.FormatUint(*st.Tasks, 10)
	}
	if st.Restarts != nil {
		restarts = strconv.FormatUint(*st.Restarts, 10)
	}
	if st.ExitStatus != nil {
		exit = strconv.Itoa(*st.ExitStatus)
	}
	started, stopped, path := st.Started, st.Stopped, st.Path
	if started == "" {
		started = dash
	}
	if stopped == "" {
		stopped = dash
	}
	if path == "" {
		path = dash
	}
	facts(c, tok, a.termFamily(),
		m.SvcLoad(), orRaw(m, svcLoadLabel(m, st.Load), st.Load),
		m.SvcActiveState(), active,
		m.SvcBoot(), boot,
		m.SvcMainPID(), pid,
		m.SvcMemory(), mem,
		m.SvcTasks(), tasks,
		m.SvcRestarts(), restarts,
		m.SvcExit(), exit,
		m.SvcStarted(), started,
		m.SvcStoppedAt(), stopped,
		m.SvcUnitFile(), path,
	)
	ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
		for _, act := range unit.Actions() {
			act := act
			if (IconButton{Icon: svcActionIcon(act), Label: svcActionLabel(m, act), Danger: act == session.ServiceStop, Disabled: s.busy != ""}).Clicked(c, "sheet:"+string(act)) {
				a.serviceDo(tc, act, unit.Name)
			}
		}
		if bootAct, ok := unit.BootAction(); ok {
			if (IconButton{Icon: svcActionIcon(bootAct), Label: svcActionLabel(m, bootAct), Disabled: s.busy != ""}).Clicked(c, "sheet:"+string(bootAct)) {
				a.serviceDo(tc, bootAct, unit.Name)
			}
		}
	})
}

func orRaw(m locales.Messages, label, raw string) string {
	if label != "" {
		return label
	}
	if raw == "" {
		return m.MetricsDash()
	}
	return raw
}

func (a *App) serviceJournal(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	s := a.services
	ui.Scroll(c.Key("journal")).Height(320).FillWidth().Radius(theme.RadiusControl).Background(tok.Fill).Children(func() {
		text := s.journal
		switch {
		case s.journalErr != "":
			text = s.journalErr
		case !s.journalBusy && strings.TrimSpace(text) == "":
			text = m.SvcNoJournal()
		}
		t := ui.Text(c, text).FontSize(theme.Caption).Selectable().Padding(10, 12)
		if a.fonts.term != "" {
			t.Font(a.fonts.term)
		}
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Spacer(c)
		if pill(c, m.Copy(), false, false, s.journalBusy || s.journal == "") {
			c.WriteClipboard(s.journal)
			c.AddToast(ui.Toast{Title: m.Copied(), Type: "success"})
		}
	})
}

func fmtBytes(n uint64) string {
	const k = 1024.0
	b := float64(n)
	switch {
	case b >= k*k*k:
		return fmt.Sprintf("%.1f GB", b/(k*k*k))
	case b >= k*k:
		return fmt.Sprintf("%.1f MB", b/(k*k))
	case b >= k:
		return fmt.Sprintf("%.1f KB", b/k)
	}
	return fmt.Sprintf("%d B", n)
}

func svcActiveLabel(m locales.Messages, state string) string {
	switch state {
	case "active":
		return m.SvcActiveActive()
	case "reloading":
		return m.SvcActiveReloading()
	case "inactive":
		return m.SvcActiveInactive()
	case "failed":
		return m.SvcActiveFailed()
	case "activating":
		return m.SvcActiveActivating()
	case "deactivating":
		return m.SvcActiveDeactivating()
	case "":
		return ""
	}
	return state
}

func svcSubLabel(m locales.Messages, state string) string {
	switch state {
	case "running":
		return m.SvcSubRunning()
	case "exited":
		return m.SvcSubExited()
	case "dead":
		return m.SvcSubDead()
	case "failed":
		return m.SvcSubFailed()
	case "waiting":
		return m.SvcSubWaiting()
	case "listening":
		return m.SvcSubListening()
	case "reload":
		return m.SvcSubReload()
	case "auto-restart":
		return m.SvcSubAutoRestart()
	case "condition":
		return m.SvcSubCondition()
	case "":
		return ""
	}
	if strings.HasPrefix(state, "start") {
		return m.SvcSubStarting()
	}
	if strings.HasPrefix(state, "stop") || strings.HasPrefix(state, "final") {
		return m.SvcSubStopping()
	}
	return state
}

func svcFileLabel(m locales.Messages, state string) (string, bool) {
	switch state {
	case "enabled":
		return m.SvcFileEnabled(), true
	case "enabled-runtime":
		return m.SvcFileEnabledRuntime(), true
	case "disabled":
		return m.SvcFileDisabled(), true
	case "static":
		return m.SvcFileStatic(), true
	case "masked", "masked-runtime":
		return m.SvcFileMasked(), true
	case "generated":
		return m.SvcFileGenerated(), true
	case "indirect":
		return m.SvcFileIndirect(), true
	case "linked", "linked-runtime":
		return m.SvcFileLinked(), true
	case "transient":
		return m.SvcFileTransient(), true
	case "alias":
		return m.SvcFileAlias(), true
	case "bad":
		return m.SvcFileBad(), true
	}
	return "", false
}

func svcLoadLabel(m locales.Messages, state string) string {
	switch state {
	case "loaded":
		return m.SvcLoadLoaded()
	case "not-found":
		return m.SvcLoadNotFound()
	case "masked":
		return m.SvcLoadMasked()
	case "error":
		return m.SvcLoadError()
	case "bad-setting":
		return m.SvcLoadBadSetting()
	}
	return ""
}

func svcSystemLabel(m locales.Messages, state string) string {
	switch state {
	case "running":
		return m.SvcSysRunning()
	case "degraded":
		return m.SvcSysDegraded()
	case "starting":
		return m.SvcSysStarting()
	case "initializing":
		return m.SvcSysInitializing()
	case "maintenance":
		return m.SvcSysMaintenance()
	case "stopping":
		return m.SvcSysStopping()
	case "offline":
		return m.SvcSysOffline()
	}
	return state
}
