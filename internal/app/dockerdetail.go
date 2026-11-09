package app

import (
	"context"
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

// The details sheet of a container: what docker inspect says of it, and,
// while it runs, its resource use and its processes, read again every
// dockerEvery while the sheet shows.

// openContainer shows the details sheet of ct.
func (a *App) openContainer(ct session.Container) {
	d := a.docker
	if d.detailRef != ct.ID {
		d.detail, d.detailRead, d.detailErr = session.ContainerDetail{}, false, nil
	}
	d.detailOpen, d.detailRef = true, ct.ID
	d.detailRunning, d.detailAt = false, time.Time{}
}

// readDetail reads the details of the container shown when it is time.
func (a *App) readDetail(c *ui.Context, tc *conn, now bool) {
	d := a.docker
	if d.detailRunning {
		return
	}
	if wait := dockerEvery - c.Now().Sub(d.detailAt); !now && !d.detailAt.IsZero() && wait > 0 {
		c.After(wait)
		return
	}
	client := tc.s.Client()
	if client == nil {
		return
	}
	d.detailRunning, d.detailAt = true, c.Now()
	gen, ref := d.gen, d.detailRef
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
		det, err := session.DockerInspect(ctx, client, ref)
		cancel()
		a.post(func() {
			if d.gen != gen || d.detailRef != ref {
				return
			}
			d.detailRunning, d.detailErr = false, err
			if err == nil {
				d.detail, d.detailRead = det, true
			}
		})
	}()
}

// dockerTime is a time docker inspect gives in the local time, "" for
// none.
func dockerTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() < 2000 {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// orDash is s, or the dash for a value there is not.
func orDash(m locales.Messages, s string) string {
	if strings.TrimSpace(s) == "" {
		return m.MetricsDash()
	}
	return s
}

// dockerDetailView is the details sheet of the container chosen.
func (a *App) dockerDetailView(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	d := a.docker
	if !d.detailOpen {
		return
	}
	i := slices.IndexFunc(d.info.Containers, func(ct session.Container) bool { return ct.ID == d.detailRef })
	if i < 0 {
		d.detailOpen = false // removed
		return
	}
	ct := d.info.Containers[i]
	a.readDetail(c, tc, false)
	w, h := c.Size()
	fam := a.termFamily()
	sheet(ui.Modal(c, &d.detailOpen, func() {
		ui.Column(c).Gap(14).Width(min(880, w-theme.Space(20))).Height(min(760, h-theme.Space(24))).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
				tile(c, icons.Docker, stateTint(tok, ct.State), 36)
				ui.Column(c).Gap(2).Grow(1).Shrink(1).MinWidth(0).Children(func() {
					sheetTitle(c, ct.Name)
					ui.Text(c, ct.Status+" · "+ct.Image).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
				})
				if d.detailRunning && !d.detailRead {
					ui.Spinner(c)
				}
				if (IconButton{Icon: icons.Refresh, Label: m.Refresh()}).Clicked(c, "detailrefresh") {
					d.detailErr = nil
					a.readDetail(c, tc, true)
				}
			})
			a.containerActions(c, m, tc, ct)
			sheetScroll(ui.Scroll(c.Key("ctrdetail")).Grow(1).Shrink(1).MinHeight(0)).Children(func() {
				ui.Column(c).Gap(12).FillWidth().Children(func() {
					if d.detailErr != nil {
						ui.Row(c.Key("detailerr")).FillWidth().Padding(8, 10).Gap(8).Radius(theme.RadiusControl).
							Background(tok.Warning.Alpha(0.12)).Border(1, tok.Warning.Alpha(0.3)).Children(func() {
							ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(tok.Warning)
							ui.Text(c, dockerErrText(m, d.detailErr)).FontSize(theme.Footnote).Shrink(1)
						})
					}
					if !d.detailRead {
						if d.detailErr == nil {
							panelNote(c, tok, m.Loading())
						}
						return
					}
					a.ctrResources(c, m, tok, fam, ct)
					a.ctrOverview(c, m, tok, fam, ct)
					a.ctrNetworks(c, m, tok, fam)
					a.ctrMounts(c, m, tok, fam)
					in := d.detail.Inspect
					a.ctrPairs(c, tok, fam, "env", icons.LocalShell, m.DockerEnv(), envPairs(in.Config.Env), true)
					a.ctrPairs(c, tok, fam, "labels", icons.FileCode, m.DockerLabels(), labelPairs(in.Config.Labels), false)
					a.ctrProcesses(c, m, tok, fam)
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Close(), true, false, false) {
					d.detailOpen = false
				}
			})
		})
	}), tok)
}

// ctrResources is the resource use of the container shown.
func (a *App) ctrResources(c *ui.Context, m locales.Messages, tok theme.Tokens, fam string, ct session.Container) {
	st := a.docker.detail.Stats
	a.panelCard(c, tok, "res", icons.Monitor, tok.TintAt(0), m.DockerResources(), "", func() {
		if !ct.Running() || st == nil {
			panelNote(c, tok, m.DockerNotRunning())
			return
		}
		ui.Row(c).Gap(theme.Space(6)).FillWidth().Children(func() {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				cpu := session.Percent(st.CPU)
				usageRow(c, tok, fam, "CPU", cpu, cpu >= 0, "")
			})
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				mem := session.Percent(st.Mem)
				usageRow(c, tok, fam, m.ColMemory(), mem, mem >= 0, st.MemUsage)
			})
		})
		pid := m.MetricsDash()
		if p := a.docker.detail.Inspect.State.Pid; p > 0 {
			pid = strconv.Itoa(p)
		}
		facts(c, tok, fam, m.DockerPIDs(), orDash(m, st.PIDs), m.DockerMainPID(), pid,
			m.DockerNetIO(), orDash(m, st.NetIO), m.DockerBlockIO(), orDash(m, st.BlockIO))
	})
}

// ctrOverview is what the container is and how it runs.
func (a *App) ctrOverview(c *ui.Context, m locales.Messages, tok theme.Tokens, fam string, ct session.Container) {
	in := a.docker.detail.Inspect
	a.panelCard(c, tok, "overview", icons.Info, tok.TintAt(1), m.DockerOverview(), "", func() {
		state := in.State.Status
		if h := in.State.Health; h != nil && h.Status != "" {
			state += " · " + m.DockerHealth() + " " + h.Status
		}
		kv := []string{
			"ID", shortID(in.ID),
			m.DockerColImage(), orDash(m, in.Config.Image),
			m.DockerColStatus(), orDash(m, state),
			m.DockerColCreated(), orDash(m, dockerTime(in.Created)),
			m.DockerStarted(), orDash(m, dockerTime(in.State.StartedAt)),
		}
		if !ct.Running() {
			code := strconv.Itoa(in.State.ExitCode)
			if in.State.OOMKilled {
				code += " · OOM"
			}
			kv = append(kv, m.DockerFinished(), orDash(m, dockerTime(in.State.FinishedAt)), m.DockerExitCode(), code)
		}
		policy := in.HostConfig.RestartPolicy.Name
		if policy == "on-failure" && in.HostConfig.RestartPolicy.Count > 0 {
			policy += ":" + strconv.Itoa(in.HostConfig.RestartPolicy.Count)
		}
		memLimit, cpuLimit := m.DockerUnlimited(), m.DockerUnlimited()
		if in.HostConfig.Memory > 0 {
			memLimit = fmtKB(uint64(in.HostConfig.Memory) / 1024)
		}
		if in.HostConfig.NanoCpus > 0 {
			cpuLimit = m.DockerCPUs(strconv.FormatFloat(float64(in.HostConfig.NanoCpus)/1e9, 'f', -1, 64))
		}
		privileged := m.DockerNo()
		if in.HostConfig.Privileged {
			privileged = m.DockerYes()
		}
		kv = append(kv,
			m.DockerRestartPolicy(), orDash(m, policy), m.DockerRestartCount(), strconv.Itoa(in.Restart),
			m.DockerUser(), orDash(m, in.Config.User), m.DockerHostname(), orDash(m, a.pv(in.Config.Hostname)),
			m.DockerNetworkMode(), orDash(m, in.HostConfig.NetworkMode), m.DockerPrivileged(), privileged,
			m.DockerMemLimit(), memLimit, m.DockerCPULimit(), cpuLimit,
		)
		facts(c, tok, fam, kv...)
		wideFact(c, tok, fam, m.DockerCommand(), orDash(m, in.Command()))
		wideFact(c, tok, fam, m.DockerWorkDir(), orDash(m, in.Config.WorkingDir))
		wideFact(c, tok, fam, m.DockerPorts(), orDash(m, a.pv(ct.Ports)))
	})
}

// wideFact is a muted label over a value that may be long: it wraps, and
// can be selected to be copied.
func wideFact(c *ui.Context, tok theme.Tokens, fam, label, value string) {
	ui.Column(c).Gap(2).FillWidth().Children(func() {
		ui.Text(c, label).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
		ui.Text(c, value).Font(fam).FontSize(theme.Footnote).Selectable()
	})
}

// ctrNetworks are the networks the container is on.
func (a *App) ctrNetworks(c *ui.Context, m locales.Messages, tok theme.Tokens, fam string) {
	ns := a.docker.detail.Inspect.Nets()
	if len(ns) == 0 {
		return
	}
	a.panelCard(c, tok, "nets", icons.Proxy, tok.TintAt(2), m.DockerNetworks(), strconv.Itoa(len(ns)), func() {
		for i, n := range ns {
			if i > 0 {
				ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			}
			ui.Text(c, n.Name).FontSize(theme.Footnote).FontWeight(600).SingleLine()
			kv := []string{"IP", orDash(m, a.pv(n.IP)), m.DockerGateway(), orDash(m, a.pv(n.Gateway)), "MAC", orDash(m, a.pv(n.MAC))}
			if n.IPv6 != "" {
				kv = append(kv, "IPv6", a.pv(n.IPv6))
			}
			facts(c, tok, fam, kv...)
		}
	})
}

// ctrMounts are the container's volumes and binds.
func (a *App) ctrMounts(c *ui.Context, m locales.Messages, tok theme.Tokens, fam string) {
	ms := a.docker.detail.Inspect.Mounts
	if len(ms) == 0 {
		return
	}
	a.panelCard(c, tok, "mounts", icons.Disk, tok.TintAt(3), m.DockerMounts(), strconv.Itoa(len(ms)), func() {
		for i, mt := range ms {
			src := mt.Source
			if mt.Type == "volume" && mt.Name != "" {
				src = mt.Name
			}
			mode := m.DockerReadWrite()
			if !mt.RW {
				mode = m.DockerReadOnly()
			}
			ui.Row(c.Key(i)).Gap(10).FillWidth().AlignItems(ui.Start).Children(func() {
				ui.Text(c, mt.Type).FontSize(theme.Caption).TextColor(tok.TextMuted).Width(52).Shrink(0).SingleLine()
				ui.Column(c).Gap(2).Grow(1).Shrink(1).MinWidth(0).Children(func() {
					ui.Text(c, mt.Destination).Font(fam).FontSize(theme.Footnote).Selectable()
					ui.Text(c, "← "+src).Font(fam).FontSize(theme.Caption).TextColor(tok.TextMuted).Selectable()
				})
				ui.Text(c, mode).FontSize(theme.Caption).TextColor(tok.TextMuted).Shrink(0).SingleLine()
			})
		}
	})
}

// envPairs splits KEY=value lines.
func envPairs(env []string) [][2]string {
	ps := make([][2]string, 0, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		ps = append(ps, [2]string{k, v})
	}
	return ps
}

// labelPairs are labels sorted by key.
func labelPairs(ls map[string]string) [][2]string {
	ps := make([][2]string, 0, len(ls))
	for k, v := range ls {
		ps = append(ps, [2]string{k, v})
	}
	slices.SortFunc(ps, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	return ps
}

// ctrPairs is a card of keys and their values, an environment or
// labels; values that may hold secrets are masked in privacy mode.
func (a *App) ctrPairs(c *ui.Context, tok theme.Tokens, fam, key string, ic *icons.Icon, title string, ps [][2]string, private bool) {
	if len(ps) == 0 {
		return
	}
	a.panelCard(c, tok, key, ic, tok.TintAt(4), title, strconv.Itoa(len(ps)), func() {
		for i, p := range ps {
			v := p[1]
			if private {
				v = a.pv(v)
			}
			ui.Row(c.Key(i)).Gap(12).FillWidth().AlignItems(ui.Start).Children(func() {
				ui.Text(c, p[0]).Font(fam).FontSize(theme.Caption).FontWeight(600).Width(220).Shrink(0).Selectable()
				ui.Text(c, v).Font(fam).FontSize(theme.Caption).TextColor(tok.TextMuted).Grow(1).Shrink(1).MinWidth(0).Selectable()
			})
		}
	})
}

// ctrProcesses are the processes in the container, as docker top lists
// them.
func (a *App) ctrProcesses(c *ui.Context, m locales.Messages, tok theme.Tokens, fam string) {
	top := a.docker.detail.Top
	if len(top.Titles) == 0 {
		return
	}
	last := len(top.Titles) - 1
	row := func(cells []string, head bool) {
		ui.Row(c).Gap(8).FillWidth().AlignItems(ui.Center).Children(func() {
			for i := range top.Titles {
				v := ""
				if i < len(cells) {
					v = cells[i]
				}
				t := ui.Text(c, v).Font(fam).FontSize(theme.Caption).SingleLine()
				if head {
					t.TextColor(tok.TextMuted).FontWeight(500)
				}
				if i == last {
					t.Grow(1).Shrink(1).MinWidth(0).Tooltip(v)
				} else {
					t.Width(64).Shrink(0)
				}
			}
		})
	}
	a.panelCard(c, tok, "procs", icons.Usage, tok.TintAt(5), m.DockerProcesses(), m.ProcessCount(len(top.Rows)), func() {
		row(top.Titles, true)
		ui.Box(c).Height(1).FillWidth().Background(tok.Line)
		for i, r := range top.Rows {
			ui.Column(c.Key(i)).FillWidth().Children(func() { row(r, false) })
		}
	})
}
