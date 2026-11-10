package app

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The Docker panel: the containers, images, networks and volumes of the
// focused session's server, read again every dockerEvery while the panel
// shows, and what can be done to them: start, stop, restart, pause,
// remove, pull an image, prune what is unused, read a container's logs,
// open a shell in it, inspect a network or a volume. Containers that
// belong to a Compose project are grouped under it, and the group's menu
// runs compose against that project. A container's or an image's details
// open in their own sheet.

// dockerEvery is how often the panel reads the server again.
const dockerEvery = 5 * time.Second

// dockerTimeout bounds one docker command, composeTimeout a compose
// command, which may pull images, and dockerLogLines how much of the
// output the logs sheet shows.
const (
	dockerTimeout  = 30 * time.Second
	composeTimeout = 2 * time.Minute // compose up may pull images
	pullTimeout    = 10 * time.Minute
	dockerLogLines = 500
)

// Which list the Docker panel shows.
const (
	dockerContainers = 0
	dockerImages     = 1
	dockerNetworks   = 2
	dockerVolumes    = 3
)

// dockerItem is a row of the containers outline: a Compose project (id
// empty) or a container, with the project it belongs to.
type dockerItem struct {
	id, project string
}

// dockerRow is what a dockerItem shows.
type dockerRow struct {
	group bool
	g     session.ComposeGroup
	ct    session.Container
}

// dockerState is what the Docker panel read, of which session, and what
// it does.
type dockerState struct {
	conn, connects, gen int
	running             bool
	at                  time.Time
	info                session.DockerInfo
	read                bool
	err                 error
	// view is the segment shown: containers, images, networks, volumes.
	view           int
	ctrSel         int
	imgSel         int
	netSel         int
	volSel         int
	ctr            ui.OutlineState[dockerItem]
	rowOf          map[dockerItem]dockerRow
	seen           map[string]bool // compose projects whose open state was chosen
	imgList        ui.ListState
	netList        ui.ListState
	volList        ui.ListState
	busy           string // the ID being acted on, "" for none
	confirm        bool   // a removal, a prune, or compose down, is asked about
	confirmRef     string
	confirmImg     bool
	confirmCompose bool
	confirmComp    session.Compose
	confirmPrune   session.DockerPrune
	logsOpen       bool
	logsRef        string
	logsName       string
	logsComp       session.Compose // set while the logs are a project's
	logs           string
	logsBusy       bool
	logsErr        string
	// The details sheet of the container detailRef, read again every
	// dockerEvery while it shows. A click on a row opens it, and the
	// row stays chosen only while it shows.
	detailOpen    bool
	detailRef     string
	detail        session.ContainerDetail
	detailRead    bool
	detailRunning bool
	detailAt      time.Time
	detailErr     error
	// The details sheet of the image imgRef, read as it opens.
	imgOpen    bool
	imgRef     string
	img        session.ImageDetail
	imgRead    bool
	imgRunning bool
	imgErr     error
	// inspect is docker inspect of a network or a volume.
	inspectOpen bool
	inspectKind session.InspectKind
	inspectRef  string
	inspectName string
	inspectText string
	inspectBusy bool
	inspectErr  string
}

// dockerErrText is a failure of the panel in words.
func dockerErrText(m locales.Messages, err error) string {
	switch {
	case errors.Is(err, session.ErrNoDocker):
		return m.DockerMissing()
	case errors.Is(err, session.ErrDockerDenied):
		return m.DockerDenied()
	}
	return m.DockerFailed(err.Error())
}

// readDocker lists tc's containers and images when it is time.
func (a *App) readDocker(c *ui.Context, tc *conn, now bool) {
	d := a.docker
	connects := tc.s.Status().Connects
	if d.conn != tc.id || d.connects != connects {
		d.gen++
		d.conn, d.connects, d.running, d.at = tc.id, connects, false, time.Time{}
		d.info, d.read, d.err, d.busy = session.DockerInfo{}, false, nil, ""
		d.ctrSel, d.imgSel, d.netSel, d.volSel = -1, -1, -1, -1
		d.inspectOpen = false
		d.ctr.Open.Clear()
		d.seen, d.rowOf = nil, nil
		d.detailOpen, d.detailRunning = false, false
		d.imgOpen, d.imgRunning = false, false
	}
	if d.running {
		return
	}
	if wait := dockerEvery - c.Now().Sub(d.at); !now && !d.at.IsZero() && wait > 0 {
		c.After(wait)
		return
	}
	if !now && errors.Is(d.err, session.ErrNoDocker) {
		return // no use asking again; the refresh button does
	}
	client := tc.s.Client()
	if client == nil {
		return
	}
	d.running, d.at = true, c.Now()
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
		info, err := session.DockerList(ctx, client)
		cancel()
		a.post(func() {
			if d.gen != gen {
				return
			}
			d.running, d.err = false, err
			if err == nil {
				d.info, d.read = info, true
			}
		})
	}()
}

// dockerDo does action to ref on tc's server, then reads the lists again.
func (a *App) dockerDo(tc *conn, action session.DockerAction, ref string) {
	d := a.docker
	client := tc.s.Client()
	if client == nil || d.busy != "" {
		return
	}
	d.busy = ref
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
		err := session.DockerDo(ctx, client, action, ref)
		cancel()
		a.post(func() {
			if d.gen != gen {
				return
			}
			d.busy, d.at, d.detailAt = "", time.Time{}, time.Time{} // read again at once
			if err != nil {
				a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
					return ui.Toast{Title: dockerErrText(m, err), Type: "error"}
				})
			}
		})
	}()
}

// dockerPull pulls the image ref, which can take a while, then reads the lists again.
func (a *App) dockerPull(tc *conn, ref string) {
	d := a.docker
	client := tc.s.Client()
	if client == nil || d.busy != "" || ref == "" {
		return
	}
	d.busy = ref
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
		err := session.DockerDo(ctx, client, session.DockerPull, ref)
		cancel()
		a.post(func() {
			if d.gen != gen {
				return
			}
			d.busy, d.at = "", time.Time{}
			if err != nil {
				a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
					return ui.Toast{Title: dockerErrText(m, err), Type: "error"}
				})
				return
			}
			name := ref
			a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
				return ui.Toast{Title: m.DockerPulled(name), Type: "success"}
			})
		})
	}()
}

// dockerPrune removes dangling images, or unused networks or volumes.
func (a *App) dockerPrune(tc *conn, p session.DockerPrune) {
	d := a.docker
	client := tc.s.Client()
	if client == nil || d.busy != "" {
		return
	}
	d.busy = "prune"
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
		out, err := session.PruneDocker(ctx, client, p)
		cancel()
		a.post(func() {
			if d.gen != gen {
				return
			}
			d.busy, d.at = "", time.Time{}
			if err != nil {
				a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
					return ui.Toast{Title: dockerErrText(m, err), Type: "error"}
				})
				return
			}
			space := session.ReclaimedSpace(out)
			a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
				title := m.DockerPruned()
				if space != "" {
					title = m.DockerPrunedSpace(space)
				}
				return ui.Toast{Title: title, Type: "success"}
			})
		})
	}()
}

// dockerInspect reads docker inspect of ref into the inspect sheet.
func (a *App) dockerInspect(tc *conn, kind session.InspectKind, ref, name string) {
	d := a.docker
	client := tc.s.Client()
	if client == nil || !session.DockerRefOK(ref) {
		return
	}
	d.logsOpen = false
	d.inspectOpen, d.inspectKind, d.inspectRef, d.inspectName = true, kind, ref, name
	d.inspectBusy, d.inspectErr, d.inspectText = true, "", ""
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
		out, err := session.InspectDocker(ctx, client, kind, ref)
		cancel()
		a.post(func() {
			if d.gen != gen || d.inspectRef != ref || d.inspectKind != kind {
				return
			}
			d.inspectBusy, d.inspectText = false, prettyJSON(out)
			if err != nil {
				d.inspectErr = err.Error()
			}
		})
	}()
}

func prettyJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	var v any
	if json.Unmarshal([]byte(s), &v) != nil {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(b)
}

// dockerLogs reads the logs of the container ref into the logs sheet.
func (a *App) dockerLogs(tc *conn, ref, name string) {
	d := a.docker
	client := tc.s.Client()
	if client == nil {
		return
	}
	d.inspectOpen = false
	d.logsOpen, d.logsRef, d.logsName, d.logsBusy, d.logsErr = true, ref, name, true, ""
	d.logsComp = session.Compose{}
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
		out, err := session.DockerLogs(ctx, client, ref, dockerLogLines)
		cancel()
		a.post(func() {
			if d.gen != gen || d.logsRef != ref {
				return
			}
			d.logsBusy, d.logs = false, out
			if err != nil {
				d.logsErr = err.Error()
			}
		})
	}()
}

// dockerCompose runs action on the project comp, then reads the lists again.
// Compose Down is asked about first; this runs it once that is confirmed.
func (a *App) dockerCompose(tc *conn, action session.ComposeAction, comp session.Compose) {
	d := a.docker
	client := tc.s.Client()
	if client == nil || d.busy != "" || !session.ComposeOK(comp) {
		return
	}
	d.busy = "compose:" + comp.Name
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
		err := session.ComposeDo(ctx, client, action, comp)
		cancel()
		a.post(func() {
			if d.gen != gen {
				return
			}
			d.busy, d.at, d.detailAt = "", time.Time{}, time.Time{}
			if err != nil {
				a.toasts = append(a.toasts, func(m locales.Messages) ui.Toast {
					return ui.Toast{Title: dockerErrText(m, err), Type: "error"}
				})
			}
		})
	}()
}

// composeLogs reads the project's logs into the logs sheet.
func (a *App) composeLogs(tc *conn, comp session.Compose) {
	d := a.docker
	client := tc.s.Client()
	if client == nil || !session.ComposeOK(comp) {
		return
	}
	ref := "compose:" + comp.Name
	d.inspectOpen = false
	d.logsOpen, d.logsRef, d.logsName, d.logsComp = true, ref, comp.Name, comp
	d.logsBusy, d.logsErr = true, ""
	gen := d.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), composeTimeout)
		out, err := session.ComposeLogs(ctx, client, comp, dockerLogLines)
		cancel()
		a.post(func() {
			if d.gen != gen || d.logsRef != ref {
				return
			}
			d.logsBusy, d.logs = false, out
			if err != nil {
				d.logsErr = err.Error()
			}
		})
	}()
}

// dockerShell opens a terminal on the host of tc in a new tab, which runs
// a shell in the container ref once it is connected: bash where the
// image has it, else sh.
func (a *App) dockerShell(tc *conn, ref string) {
	if !session.DockerRefOK(ref) {
		return
	}
	docker := "docker"
	if a.docker.info.Sudo {
		docker = "sudo docker"
	}
	// The container's shell is typed once, without the host's tmux
	// session.
	a.openTerminal(tc.host.ID, func(cn *conn) {
		if cn.start != nil {
			cn.start.once = docker + " exec -it " + ref + ` sh -c "command -v bash >/dev/null && exec bash || exec sh"`
			cn.start.host = ""
		}
	})
}

// dockerPanel is the Docker panel's body.
func (a *App) dockerPanel(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	if a.docker == nil {
		d := &dockerState{ctrSel: -1, imgSel: -1, netSel: -1, volSel: -1, imgList: ui.ListState{RoundRows: true}, netList: ui.ListState{RoundRows: true}, volList: ui.ListState{RoundRows: true}}
		d.ctr.List.RoundRows = true
		a.docker = d
	}
	d := a.docker
	cn := a.focused()
	tc := a.sshConn(cn)
	if note := a.usageNote(m, cn, tc); note != "" {
		panelNote(c, tok, note)
		return
	}
	a.readDocker(c, tc, false)
	ui.Row(c.Key("dockertools")).Gap(8).FillWidth().AlignItems(ui.Center).Children(func() {
		formSegmented(c.Key("dockerview"), &d.view, false,
			m.DockerContainers(strconv.Itoa(len(d.info.Containers))),
			m.DockerImages(strconv.Itoa(len(d.info.Images))),
			m.DockerNetworkList(strconv.Itoa(len(d.info.Networks))),
			m.DockerVolumeList(strconv.Itoa(len(d.info.Volumes)))).
			Label(m.DockerManager()).Grow(1).Shrink(1).MinWidth(0)
		if d.running {
			ui.Spinner(c)
		}
		if p, ok := dockerPruneOf(d.view); ok {
			if (IconButton{Icon: icons.Prune, Label: dockerPruneLabel(m, p), Disabled: d.running || d.busy != "" || !d.read}).Clicked(c, "dockerprune") {
				d.confirm, d.confirmImg, d.confirmCompose, d.confirmPrune = true, false, false, p
			}
		}
		if (IconButton{Icon: icons.Refresh, Label: m.Refresh(), Disabled: d.running}).Clicked(c, "dockerrefresh") {
			d.err = nil
			a.readDocker(c, tc, true)
		}
	})
	switch {
	case d.err != nil:
		ui.Row(c.Key("dockererr")).FillWidth().Padding(8, 10).Gap(8).Radius(theme.RadiusControl).
			Background(tok.Warning.Alpha(0.12)).Border(1, tok.Warning.Alpha(0.3)).Children(func() {
			ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(tok.Warning)
			ui.Text(c, a.pv(dockerErrText(m, d.err))).FontSize(theme.Footnote).Shrink(1)
		})
		if !d.read {
			return
		}
	case !d.read:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			panelNote(c, tok, m.MonitorWaiting())
		})
		return
	}
	switch d.view {
	case dockerImages:
		a.imagesView(c, m, tok, tc)
	case dockerNetworks:
		a.networksView(c, m, tok, tc)
	case dockerVolumes:
		a.volumesView(c, m, tok, tc)
	default:
		a.containersView(c, m, tok, tc)
	}
	a.dockerImageView(c, m, tok, tc)
	a.dockerDetailView(c, m, tok, tc)
	a.dockerConfirm(c, m, tc)
	a.dockerLogsView(c, m, tok, tc)
	a.dockerInspectView(c, m, tok, tc)
}

func dockerPruneOf(view int) (session.DockerPrune, bool) {
	switch view {
	case dockerImages:
		return session.PruneImages, true
	case dockerNetworks:
		return session.PruneNetworks, true
	case dockerVolumes:
		return session.PruneVolumes, true
	}
	return "", false
}

func dockerPruneLabel(m locales.Messages, p session.DockerPrune) string {
	switch p {
	case session.PruneNetworks:
		return m.DockerPruneNetworks()
	case session.PruneVolumes:
		return m.DockerPruneVolumes()
	}
	return m.DockerPruneImages()
}

// stateTint is the color of a container's state.
func stateTint(tok theme.Tokens, state string) ui.Color {
	switch state {
	case "running":
		return tok.Success
	case "paused", "restarting", "created":
		return tok.Warning
	case "dead":
		return tok.Danger
	}
	return tok.TextMuted
}

func (a *App) containersView(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	d := a.docker
	cs := d.info.Containers
	if len(cs) == 0 {
		panelNote(c, tok, m.DockerNoContainers())
		return
	}
	roots, children, rowOf := composeTree(session.GroupCompose(cs))
	d.rowOf = rowOf
	a.openNewCompose(roots)
	// A container stays chosen only while its details show. A Compose
	// project stays chosen so its card keeps showing.
	if d.ctrSel >= 0 && d.ctrSel < d.ctr.Rows() {
		if row, ok := d.rowOf[d.ctr.Item(d.ctrSel)]; !ok || (!row.group && !d.detailOpen) {
			d.ctrSel = -1
		}
	} else {
		d.ctrSel = -1
	}
	d.ctr.List.RoundRows = true
	d.ctr.List.Selected = &d.ctrSel
	d.ctr.List.Label = func(i int) string {
		if i < 0 || i >= d.ctr.Rows() {
			return ""
		}
		if row, ok := d.rowOf[d.ctr.Item(i)]; ok {
			if row.group {
				return row.g.Compose.Name
			}
			return row.ct.Name
		}
		return ""
	}
	cols := []ui.TableColumn{{Title: m.ColName(), MinWidth: 90}, {Title: m.DockerColImage(), Width: 120}, {Title: m.DockerColStatus(), Width: 120}}
	tbl := ui.OutlineTable(c.Key("containers"), &d.ctr, cols, roots, children, func(item dockerItem, col int) {
		row, ok := d.rowOf[item]
		if !ok {
			return
		}
		muted := tok.TextMuted
		if d.ctrSel >= 0 && d.ctrSel < d.ctr.Rows() && d.ctr.Item(d.ctrSel) == item {
			muted = tok.AccentText
		}
		cell := ui.Row(c).Gap(6).AlignItems(ui.Center).Grow(1).MinWidth(0).AlignSelf(ui.Stretch).Children(func() {
			if row.group {
				a.composeGroupCell(c, m, tok, row.g, col, muted)
				return
			}
			ct := row.ct
			switch col {
			case 0:
				dot(c, stateTint(tok, ct.State))
				ui.Text(c, ct.Name).FontSize(theme.Footnote).SingleLine()
			case 1:
				ui.Text(c, ct.Image).FontSize(theme.Footnote).TextColor(muted).SingleLine()
			case 2:
				ui.Text(c, ct.Status).FontSize(theme.Footnote).TextColor(muted).SingleLine()
			}
		})
		if row.group {
			comp := row.g.Compose
			a.contextMenu(cell, func() []menuEntry { return a.composeEntries(m, tc, comp) })
			return
		}
		id := row.ct.ID
		a.contextMenu(cell, func() []menuEntry { return a.containerEntries(m, tc, id) })
	}).Grow(1).Shrink(1).MinHeight(0).FillWidth()
	if (tbl.Changed() || tbl.Submitted()) && d.ctrSel >= 0 && d.ctrSel < d.ctr.Rows() {
		if row, ok := d.rowOf[d.ctr.Item(d.ctrSel)]; ok {
			if row.group {
				d.detailOpen = false
			} else {
				d.imgOpen = false
				a.openContainer(row.ct)
			}
		}
	}
	if d.ctrSel < 0 || d.ctrSel >= d.ctr.Rows() {
		panelNote(c, tok, m.DockerPickContainer())
		return
	}
	row, ok := d.rowOf[d.ctr.Item(d.ctrSel)]
	if ok && row.group {
		a.composeCard(c, m, tok, tc, row.g)
	}
}

// composeTree is the outline: projects and containers that belong to none
// at the top, a project's containers beneath it.
func composeTree(groups []session.ComposeGroup) (roots []dockerItem, children func(dockerItem) []dockerItem, rowOf map[dockerItem]dockerRow) {
	rowOf = map[dockerItem]dockerRow{}
	kids := map[dockerItem][]dockerItem{}
	for _, g := range groups {
		if g.Project() {
			p := dockerItem{project: g.Compose.Name}
			roots = append(roots, p)
			rowOf[p] = dockerRow{group: true, g: g}
			var ch []dockerItem
			for _, ct := range g.Containers {
				it := dockerItem{id: ct.ID, project: g.Compose.Name}
				ch = append(ch, it)
				rowOf[it] = dockerRow{ct: ct}
			}
			kids[p] = ch
			continue
		}
		ct := g.Containers[0]
		it := dockerItem{id: ct.ID}
		roots = append(roots, it)
		rowOf[it] = dockerRow{ct: ct}
	}
	children = func(it dockerItem) []dockerItem { return kids[it] }
	return roots, children, rowOf
}

// openNewCompose opens a project the first time it is seen, and leaves one
// the user closed shut.
func (a *App) openNewCompose(roots []dockerItem) {
	d := a.docker
	if d.seen == nil {
		d.seen = map[string]bool{}
	}
	for _, it := range roots {
		if it.id != "" || d.seen[it.project] {
			continue
		}
		d.seen[it.project] = true
		d.ctr.Open.Add(it)
	}
}

func composeTint(tok theme.Tokens, g session.ComposeGroup) ui.Color {
	run := 0
	for _, ct := range g.Containers {
		if ct.State == "running" {
			run++
		}
	}
	switch {
	case run > 0 && run == len(g.Containers):
		return tok.Success
	case run == 0:
		return tok.TextMuted
	}
	return tok.Warning
}

func (a *App) composeGroupCell(c *ui.Context, m locales.Messages, tok theme.Tokens, g session.ComposeGroup, col int, muted ui.Color) {
	switch col {
	case 0:
		ui.Icon(c, icons.Compose.SVG).Size(14, 14).Shrink(0).TextColor(composeTint(tok, g))
		ui.Text(c, g.Compose.Name).FontSize(theme.Footnote).FontWeight(600).SingleLine()
	case 1:
		label := unixBase(g.Compose.Dir)
		tip := g.Compose.Dir
		if len(g.Compose.Files) > 0 {
			label = unixBase(g.Compose.Files[0])
			tip = strings.Join(g.Compose.Files, ", ")
		}
		if label == "" {
			label = m.DockerComposeFile()
		}
		text := ui.Text(c, label).FontSize(theme.Footnote).TextColor(muted).SingleLine()
		if tip != "" {
			text.Tooltip(tip)
		}
	case 2:
		run := 0
		for _, ct := range g.Containers {
			if ct.State == "running" {
				run++
			}
		}
		ui.Text(c, m.DockerComposeRunning(strconv.Itoa(run), strconv.Itoa(len(g.Containers)))).FontSize(theme.Footnote).TextColor(muted).SingleLine()
	}
}

// unixBase is the last piece of a remote path. filepath.Base would keep a
// Unix path whole on Windows.
func unixBase(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return p
}

func (a *App) composeCard(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn, g session.ComposeGroup) {
	comp := g.Compose
	a.contentCard(c.Key("projcard"), tok).FillWidth().Padding(theme.Space(3)).Gap(theme.Space(2)).Children(func() {
		run := 0
		for _, ct := range g.Containers {
			if ct.State == "running" {
				run++
			}
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			softTile(c, icons.Compose, composeTint(tok, g), 22)
			ui.Column(c).Gap(1).Grow(1).Shrink(1).MinWidth(0).Children(func() {
				ui.Text(c, comp.Name).FontSize(theme.Body).FontWeight(600).SingleLine()
				ui.Text(c, m.DockerComposeRunning(strconv.Itoa(run), strconv.Itoa(len(g.Containers)))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
			})
		})
		dir, files := comp.Dir, strings.Join(comp.Files, ", ")
		if dir == "" {
			dir = m.MetricsDash()
		}
		if files == "" {
			files = m.MetricsDash()
		}
		facts(c, tok, a.termFamily(), m.DockerComposeDir(), dir, m.DockerComposeFile(), files)
		a.composeButtons(c, m, tc, comp)
	})
}

// composeButtons are the compose commands, on a project's card.
func (a *App) composeButtons(c *ui.Context, m locales.Messages, tc *conn, comp session.Compose) {
	d := a.docker
	busy := d.busy != "" || !session.ComposeOK(comp)
	ui.Row(c.Key("projacts")).Gap(2).AlignItems(ui.Center).FillWidth().Children(func() {
		if (IconButton{Icon: icons.Resume, Label: m.DockerComposeUp(), Disabled: busy}).Clicked(c, "up") {
			a.dockerCompose(tc, session.ComposeUp, comp)
		}
		if (IconButton{Icon: icons.Reconnect, Label: m.DockerComposeRestart(), Disabled: busy}).Clicked(c, "restart") {
			a.dockerCompose(tc, session.ComposeRestart, comp)
		}
		if (IconButton{Icon: icons.Halt, Label: m.DockerComposeStop(), Disabled: busy}).Clicked(c, "stop") {
			a.dockerCompose(tc, session.ComposeStop, comp)
		}
		if (IconButton{Icon: icons.Resume, Label: m.DockerComposeStart(), Disabled: busy}).Clicked(c, "start") {
			a.dockerCompose(tc, session.ComposeStart, comp)
		}
		if (IconButton{Icon: icons.Logs, Label: m.DockerComposeLogs()}).Clicked(c, "logs") {
			a.composeLogs(tc, comp)
		}
		ui.Spacer(c)
		if d.busy != "" {
			ui.Spinner(c)
		}
		if (IconButton{Icon: icons.Delete, Label: m.DockerComposeDown(), Danger: true, Disabled: busy}).Clicked(c, "down") {
			d.askConfirm(comp.Name, false, true, comp)
		}
	})
}

// composeEntries are the compose commands of a project's menu.
func (a *App) composeEntries(m locales.Messages, tc *conn, comp session.Compose) []menuEntry {
	d := a.docker
	off := d.busy != "" || !session.ComposeOK(comp)
	return []menuEntry{
		{Icon: icons.Resume, Label: m.DockerComposeUp(), Disabled: off, Do: func() { a.dockerCompose(tc, session.ComposeUp, comp) }},
		{Icon: icons.Delete, Label: m.DockerComposeDown(), Danger: true, Disabled: off, Do: func() {
			d.confirm, d.confirmRef, d.confirmImg, d.confirmCompose = true, comp.Name, false, true
			d.confirmComp = comp
		}},
		{Icon: icons.Reconnect, Label: m.DockerComposeRestart(), Disabled: off, Do: func() { a.dockerCompose(tc, session.ComposeRestart, comp) }},
		{Icon: icons.Halt, Label: m.DockerComposeStop(), Disabled: off, Do: func() { a.dockerCompose(tc, session.ComposeStop, comp) }},
		{Icon: icons.Resume, Label: m.DockerComposeStart(), Disabled: off, Do: func() { a.dockerCompose(tc, session.ComposeStart, comp) }},
		menuSep,
		{Icon: icons.Logs, Label: m.DockerComposeLogs(), Disabled: !session.ComposeOK(comp), Do: func() { a.composeLogs(tc, comp) }},
	}
}

// containerActions are the buttons doing to the container ct what its
// menu does.
func (a *App) containerActions(c *ui.Context, m locales.Messages, tc *conn, ct session.Container) {
	d := a.docker
	ui.Row(c.Key("ctracts")).Gap(2).AlignItems(ui.Center).FillWidth().Children(func() {
		busy := d.busy != ""
		if ct.Running() {
			if (IconButton{Icon: icons.Halt, Label: m.DockerStop(), Disabled: busy}).Clicked(c, "stop") {
				a.dockerDo(tc, session.DockerStop, ct.ID)
			}
		} else if (IconButton{Icon: icons.Resume, Label: m.DockerStart(), Disabled: busy}).Clicked(c, "start") {
			a.dockerDo(tc, session.DockerStart, ct.ID)
		}
		if (IconButton{Icon: icons.Reconnect, Label: m.DockerRestart(), Disabled: busy}).Clicked(c, "restart") {
			a.dockerDo(tc, session.DockerRestart, ct.ID)
		}
		if ct.State == "paused" {
			if (IconButton{Icon: icons.Resume, Label: m.DockerUnpause(), Disabled: busy, Active: true}).Clicked(c, "unpause") {
				a.dockerDo(tc, session.DockerUnpause, ct.ID)
			}
		} else if (IconButton{Icon: icons.Pause, Label: m.DockerPause(), Disabled: busy || ct.State != "running"}).Clicked(c, "pause") {
			a.dockerDo(tc, session.DockerPause, ct.ID)
		}
		if (IconButton{Icon: icons.Logs, Label: m.DockerLogs()}).Clicked(c, "logs") {
			a.dockerLogs(tc, ct.ID, ct.Name)
		}
		if (IconButton{Icon: icons.Terminal, Label: m.DockerShell(), Disabled: ct.State != "running"}).Clicked(c, "shell") {
			d.detailOpen = false
			a.dockerShell(tc, ct.ID)
		}
		ui.Spacer(c)
		if busy {
			ui.Spinner(c)
		}
		if (IconButton{Icon: icons.Delete, Label: m.DockerRemove(), Danger: true, Disabled: busy}).Clicked(c, "rm") {
			d.confirm, d.confirmRef, d.confirmImg, d.confirmCompose = true, ct.ID, false, false
		}
	})
}

// containerEntries are the menu of the container id: what the buttons
// of its details do. None once it is gone.
func (a *App) containerEntries(m locales.Messages, tc *conn, id string) []menuEntry {
	d := a.docker
	i := slices.IndexFunc(d.info.Containers, func(ct session.Container) bool { return ct.ID == id })
	if i < 0 {
		return nil
	}
	ct := d.info.Containers[i]
	busy := d.busy != ""
	do := func(action session.DockerAction) func() { return func() { a.dockerDo(tc, action, ct.ID) } }
	es := []menuEntry{{Icon: icons.Info, Label: m.DockerDetails(), Do: func() {
		d.imgOpen = false
		a.openContainer(ct)
	}}, menuSep}
	if ct.Running() {
		es = append(es, menuEntry{Icon: icons.Halt, Label: m.DockerStop(), Disabled: busy, Do: do(session.DockerStop)})
	} else {
		es = append(es, menuEntry{Icon: icons.Resume, Label: m.DockerStart(), Disabled: busy, Do: do(session.DockerStart)})
	}
	es = append(es, menuEntry{Icon: icons.Reconnect, Label: m.DockerRestart(), Disabled: busy, Do: do(session.DockerRestart)})
	if ct.State == "paused" {
		es = append(es, menuEntry{Icon: icons.Resume, Label: m.DockerUnpause(), Disabled: busy, Do: do(session.DockerUnpause)})
	} else {
		es = append(es, menuEntry{Icon: icons.Pause, Label: m.DockerPause(), Disabled: busy || ct.State != "running", Do: do(session.DockerPause)})
	}
	es = append(es, menuSep,
		menuEntry{Icon: icons.Logs, Label: m.DockerLogs(), Do: func() { a.dockerLogs(tc, ct.ID, ct.Name) }},
		menuEntry{Icon: icons.Terminal, Label: m.DockerShell(), Disabled: ct.State != "running", Do: func() {
			d.detailOpen, d.imgOpen = false, false
			a.dockerShell(tc, ct.ID)
		}},
		menuSep,
		menuEntry{Icon: icons.Delete, Label: m.DockerRemove(), Danger: true, Disabled: busy, Do: func() {
			d.confirm, d.confirmRef, d.confirmImg, d.confirmCompose = true, ct.ID, false, false
		}},
	)
	if comp, ok := ct.Compose(); ok {
		es = append(es, menuSep)
		es = append(es, a.composeEntries(m, tc, comp)...)
	}
	return es
}

// imageEntries are the menu of the image id. None once it is gone.
func (a *App) imageEntries(m locales.Messages, tc *conn, id string) []menuEntry {
	d := a.docker
	i := slices.IndexFunc(d.info.Images, func(im session.Image) bool { return im.ID == id })
	if i < 0 {
		return nil
	}
	im := d.info.Images[i]
	return []menuEntry{
		{Icon: icons.Info, Label: m.DockerDetails(), Do: func() { a.openImage(tc, id) }},
		{Icon: icons.Download, Label: m.DockerPull(), Disabled: d.busy != "" || im.PullRef() == "", Do: func() {
			a.dockerPull(tc, im.PullRef())
		}},
		menuSep,
		{Icon: icons.Delete, Label: m.DockerRemoveImage(), Danger: true, Disabled: d.busy != "", Do: func() {
			d.confirm, d.confirmRef, d.confirmImg, d.confirmCompose = true, id, true, false
		}},
	}
}

func (a *App) imagesView(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	d := a.docker
	is := d.info.Images
	if len(is) == 0 {
		panelNote(c, tok, m.DockerNoImages())
		return
	}
	if !d.imgOpen || d.imgSel >= len(is) {
		d.imgSel = -1
	}
	d.imgList.Selected = &d.imgSel
	cols := []ui.TableColumn{{Title: m.DockerColImage(), MinWidth: 120}, {Title: m.ColSize(), Width: 72, Align: ui.End}, {Title: m.DockerColCreated(), Width: 110}}
	tbl := ui.Table(c.Key("images"), &d.imgList, cols, len(is), func(row, col int) {
		im := is[row]
		muted := tok.TextMuted
		if d.imgSel == row {
			muted = tok.AccentText
		}
		cell := ui.Row(c).AlignItems(ui.Center).Grow(1).MinWidth(0).AlignSelf(ui.Stretch).Children(func() {
			switch col {
			case 0:
				ui.Text(c, im.Ref()).FontSize(theme.Footnote).SingleLine().Tooltip(im.Ref() + " · " + shortID(im.ID))
			case 1:
				ui.Spacer(c)
				ui.Text(c, im.Size).FontSize(theme.Footnote).TextColor(muted).SingleLine()
			case 2:
				ui.Text(c, im.Created).FontSize(theme.Footnote).TextColor(muted).SingleLine()
			}
		})
		a.contextMenu(cell, func() []menuEntry { return a.imageEntries(m, tc, im.ID) })
	}).Grow(1).Shrink(1).MinHeight(0).FillWidth()
	if (tbl.Changed() || tbl.Submitted()) && d.imgSel >= 0 && d.imgSel < len(is) {
		a.openImage(tc, is[d.imgSel].ID)
	}
	if d.imgSel < 0 {
		panelNote(c, tok, m.DockerPickImage())
	}
}

func (a *App) networksView(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	a.dockerObjects(c, m, tok, tc, true)
}

func (a *App) volumesView(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	a.dockerObjects(c, m, tok, tc, false)
}

// dockerObjects is the networks list, or the volumes list: a click opens
// docker inspect.
func (a *App) dockerObjects(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn, networks bool) {
	d := a.docker
	n := len(d.info.Volumes)
	if networks {
		n = len(d.info.Networks)
	}
	if n == 0 {
		if networks {
			panelNote(c, tok, m.DockerNoNetworks())
		} else {
			panelNote(c, tok, m.DockerNoVolumes())
		}
		return
	}
	list := &d.volList
	sel := &d.volSel
	kind := session.InspectVolume
	key := "volumes"
	if networks {
		list, sel, kind, key = &d.netList, &d.netSel, session.InspectNetwork, "networks"
	}
	if *sel >= n {
		*sel = -1
	}
	list.Selected = sel
	cols := []ui.TableColumn{{Title: m.ColName(), MinWidth: 120}, {Title: m.DockerColDriver(), Width: 80}, {Title: m.DockerColScope(), Width: 72}}
	ui.Table(c.Key(key), list, cols, n, func(row, col int) {
		name, driver, scope, tip := a.dockerObject(networks, row)
		muted := tok.TextMuted
		if *sel == row {
			muted = tok.AccentText
		}
		switch col {
		case 0:
			cell := ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Cursor(ui.CursorPointer)
			cell.Children(func() {
				ui.Text(c, name).FontSize(theme.Footnote).SingleLine().Tooltip(tip)
			})
			ref, b := name, cell.Bounds()
			cell.HandleInput(func(ev ui.InputEvent) bool {
				if ev.Kind == ui.InputPointerDown && ev.Button == 0 && ref != "" {
					a.dockerInspect(tc, kind, ref, ref)
					return false
				}
				return a.contextPress(b, ev, func() []menuEntry {
					if ref == "" {
						return nil
					}
					return []menuEntry{{Icon: icons.Info, Label: m.DockerInspect(), Do: func() {
						a.dockerInspect(tc, kind, ref, ref)
					}}}
				})
			})
		case 1:
			ui.Text(c, orDash(m, driver)).FontSize(theme.Footnote).TextColor(muted).SingleLine()
		case 2:
			ui.Text(c, orDash(m, scope)).FontSize(theme.Footnote).TextColor(muted).SingleLine()
		}
	}).Grow(1).Shrink(1).MinHeight(0).FillWidth()
}

func (a *App) dockerObject(networks bool, row int) (name, driver, scope, tip string) {
	d := a.docker
	if networks {
		if row < 0 || row >= len(d.info.Networks) {
			return
		}
		n := d.info.Networks[row]
		tip = n.Name
		if n.ID != "" {
			tip += " · " + shortID(n.ID)
		}
		return n.Name, n.Driver, n.Scope, tip
	}
	if row < 0 || row >= len(d.info.Volumes) {
		return
	}
	v := d.info.Volumes[row]
	tip = v.Name
	if v.Mountpoint != "" {
		tip += " · " + v.Mountpoint
	}
	return v.Name, v.Driver, v.Scope, tip
}

func (d *dockerState) askConfirm(ref string, img, compose bool, comp session.Compose) {
	d.confirm, d.confirmRef, d.confirmImg, d.confirmCompose = true, ref, img, compose
	d.confirmComp, d.confirmPrune = comp, ""
}

// dockerConfirm asks before a container or an image is removed, a prune
// runs, or a Compose project is taken down.
func (a *App) dockerConfirm(c *ui.Context, m locales.Messages, tc *conn) {
	d := a.docker
	if !d.confirm {
		return
	}
	if d.confirmPrune != "" {
		title, msg := m.DockerPruneImages(), m.DockerPruneImagesConfirm()
		switch d.confirmPrune {
		case session.PruneNetworks:
			title, msg = m.DockerPruneNetworks(), m.DockerPruneNetworksConfirm()
		case session.PruneVolumes:
			title, msg = m.DockerPruneVolumes(), m.DockerPruneVolumesConfirm()
		}
		if ui.AlertDialog(c, &d.confirm, title, msg, m.Cancel(), m.DockerPrune()) != 1 {
			return
		}
		a.dockerPrune(tc, d.confirmPrune)
		return
	}
	title, msg := m.DockerRemove(), m.DockerRemoveConfirm(a.dockerName(d.confirmRef))
	switch {
	case d.confirmCompose:
		title, msg = m.DockerComposeDown(), m.DockerComposeDownConfirm(d.confirmComp.Name)
	case d.confirmImg:
		title, msg = m.DockerRemoveImage(), m.DockerRemoveImageConfirm(a.dockerName(d.confirmRef))
	}
	if ui.AlertDialog(c, &d.confirm, title, msg, m.Cancel(), m.Delete()) != 1 {
		return
	}
	if d.confirmCompose {
		a.dockerCompose(tc, session.ComposeDown, d.confirmComp)
		return
	}
	action := session.DockerRemove
	if d.confirmImg {
		action = session.DockerRemoveImage
	}
	a.dockerDo(tc, action, d.confirmRef)
}

// dockerName is how the panel names the container or image id.
func (a *App) dockerName(id string) string {
	for _, ct := range a.docker.info.Containers {
		if ct.ID == id {
			return ct.Name
		}
	}
	for _, im := range a.docker.info.Images {
		if im.ID == id {
			return im.Ref()
		}
	}
	return shortID(id)
}

// shortID is a Docker ID as docker shows it: 12 hex digits, without the
// "sha256:".
func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// dockerInspectView is the sheet of docker inspect for an image, a
// network or a volume.
func (a *App) dockerInspectView(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	d := a.docker
	if !d.inspectOpen {
		return
	}
	sheet(ui.Modal(c, &d.inspectOpen, func() {
		ui.Column(c).Gap(12).Width(720).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
				tile(c, icons.Info, theme.Brand, 36)
				ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
					sheetTitle(c, m.DockerInspectOf(d.inspectName))
					ui.Text(c, m.DockerInspect()).FontSize(theme.Caption).TextColor(tok.TextMuted)
				})
				if d.inspectBusy {
					ui.Spinner(c)
				}
				if (IconButton{Icon: icons.Refresh, Label: m.Refresh(), Disabled: d.inspectBusy}).Clicked(c, "inspectrefresh") {
					a.dockerInspect(tc, d.inspectKind, d.inspectRef, d.inspectName)
				}
			})
			ui.Scroll(c.Key("inspect")).Height(420).FillWidth().Radius(theme.RadiusControl).Background(tok.Fill).Children(func() {
				text := d.inspectText
				switch {
				case d.inspectErr != "":
					text = d.inspectErr
				case !d.inspectBusy && strings.TrimSpace(text) == "":
					text = m.DockerNoInspect()
				}
				t := ui.Text(c, text).FontSize(theme.Caption).Selectable().Padding(10, 12)
				if a.fonts.term != "" {
					t.Font(a.fonts.term)
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Copy(), false, false, d.inspectBusy || d.inspectText == "") {
					c.WriteClipboard(d.inspectText)
					c.AddToast(ui.Toast{Title: m.Copied(), Type: "success"})
				}
				if pill(c, m.Close(), true, false, false) {
					d.inspectOpen = false
				}
			})
		})
	}), tok)
}

// dockerLogsView is the sheet of a container's last lines of output.
func (a *App) dockerLogsView(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	d := a.docker
	if !d.logsOpen {
		return
	}
	sheet(ui.Modal(c, &d.logsOpen, func() {
		ui.Column(c).Gap(12).Width(720).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
				tile(c, icons.Logs, theme.Brand, 36)
				ui.Column(c).Gap(2).Grow(1).Shrink(1).Children(func() {
					title := m.DockerLogsOf(d.logsName)
					if strings.HasPrefix(d.logsRef, "compose:") {
						title = m.DockerComposeLogsOf(d.logsName)
					}
					sheetTitle(c, title)
					ui.Text(c, m.DockerLogsNote(strconv.Itoa(dockerLogLines))).FontSize(theme.Caption).TextColor(tok.TextMuted)
				})
				if d.logsBusy {
					ui.Spinner(c)
				}
				if (IconButton{Icon: icons.Refresh, Label: m.Refresh(), Disabled: d.logsBusy}).Clicked(c, "logsrefresh") {
					if strings.HasPrefix(d.logsRef, "compose:") {
						a.composeLogs(tc, d.logsComp)
					} else {
						a.dockerLogs(tc, d.logsRef, d.logsName)
					}
				}
			})
			ui.Scroll(c.Key("logs")).Height(420).FillWidth().Radius(theme.RadiusControl).Background(tok.Fill).Children(func() {
				text := d.logs
				switch {
				case d.logsErr != "":
					text = d.logsErr
				case !d.logsBusy && strings.TrimSpace(text) == "":
					text = m.DockerNoLogs()
				}
				t := ui.Text(c, text).FontSize(theme.Caption).Selectable().Padding(10, 12)
				if a.fonts.term != "" {
					t.Font(a.fonts.term)
				}
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Copy(), false, false, d.logsBusy || d.logs == "") {
					c.WriteClipboard(d.logs)
					c.AddToast(ui.Toast{Title: m.Copied(), Type: "success"})
				}
				if pill(c, m.Close(), true, false, false) {
					d.logsOpen = false
				}
			})
		})
	}), tok)
}
