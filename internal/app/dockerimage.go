package app

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/session"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

// The details sheet of an image: what docker image inspect says of it,
// the steps that built it and the containers made from it, read as it
// opens and again on Refresh.

// openImage shows the details sheet of the image id and reads them.
func (a *App) openImage(tc *conn, id string) {
	d := a.docker
	if d.imgRef != id {
		d.img, d.imgRead, d.imgErr = session.ImageDetail{}, false, nil
	}
	d.imgOpen, d.imgRef, d.imgRunning = true, id, false
	a.readImage(tc)
}

// readImage reads the details of the image shown.
func (a *App) readImage(tc *conn) {
	d := a.docker
	client := tc.s.Client()
	if client == nil || d.imgRunning {
		return
	}
	d.imgRunning = true
	gen, ref := d.gen, d.imgRef
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), dockerTimeout)
		det, err := session.DockerImageInspect(ctx, client, ref)
		cancel()
		a.post(func() {
			if d.gen != gen || d.imgRef != ref {
				return
			}
			d.imgRunning, d.imgErr = false, err
			if err == nil {
				d.img, d.imgRead = det, true
			}
		})
	}()
}

// dockerImageView is the details sheet of the image chosen.
func (a *App) dockerImageView(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	d := a.docker
	if !d.imgOpen {
		return
	}
	i := slices.IndexFunc(d.info.Images, func(im session.Image) bool { return im.ID == d.imgRef })
	if i < 0 {
		d.imgOpen = false // removed
		return
	}
	im := d.info.Images[i]
	w, h := c.Size()
	fam := a.termFamily()
	sheet(ui.Modal(c, &d.imgOpen, func() {
		ui.Column(c).Gap(14).Width(min(880, w-theme.Space(20))).Height(min(760, h-theme.Space(24))).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
				tile(c, icons.Docker, theme.Brand, 36)
				ui.Column(c).Gap(2).Grow(1).Shrink(1).MinWidth(0).Children(func() {
					sheetTitle(c, im.Ref())
					ui.Text(c, im.Size+" · "+im.Created).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine()
				})
				if d.imgRunning {
					ui.Spinner(c)
				}
				if (IconButton{Icon: icons.Refresh, Label: m.Refresh(), Disabled: d.imgRunning}).Clicked(c, "imgrefresh") {
					d.imgErr = nil
					a.readImage(tc)
				}
				if d.busy != "" {
					ui.Spinner(c)
				}
				if (IconButton{Icon: icons.Delete, Label: m.DockerRemoveImage(), Danger: true, Disabled: d.busy != ""}).Clicked(c, "rmi") {
					d.confirm, d.confirmRef, d.confirmImg = true, im.ID, true
				}
			})
			sheetScroll(ui.Scroll(c.Key("imgdetail")).Grow(1).Shrink(1).MinHeight(0)).Children(func() {
				ui.Column(c).Gap(12).FillWidth().Children(func() {
					if d.imgErr != nil {
						ui.Row(c.Key("imgerr")).FillWidth().Padding(8, 10).Gap(8).Radius(theme.RadiusControl).
							Background(tok.Warning.Alpha(0.12)).Border(1, tok.Warning.Alpha(0.3)).Children(func() {
							ui.Icon(c, icons.Warning.SVG).Size(14, 14).Shrink(0).TextColor(tok.Warning)
							ui.Text(c, dockerErrText(m, d.imgErr)).FontSize(theme.Footnote).Shrink(1)
						})
					}
					if !d.imgRead {
						if d.imgErr == nil {
							panelNote(c, tok, m.Loading())
						}
						return
					}
					in := d.img.Inspect
					a.imgOverview(c, m, tok, fam, im)
					a.imgContainers(c, m, tok, tc)
					a.ctrPairs(c, tok, fam, "env", icons.LocalShell, m.DockerEnv(), envPairs(in.Config.Env), true)
					a.ctrPairs(c, tok, fam, "labels", icons.FileCode, m.DockerLabels(), labelPairs(in.Config.Labels), false)
					a.imgHistory(c, m, tok, fam)
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				ui.Spacer(c)
				if pill(c, m.Close(), true, false, false) {
					d.imgOpen = false
				}
			})
		})
	}), tok)
}

// imgOverview is what the image is and what it runs.
func (a *App) imgOverview(c *ui.Context, m locales.Messages, tok theme.Tokens, fam string, im session.Image) {
	in := a.docker.img.Inspect
	a.panelCard(c, tok, "overview", icons.Info, tok.TintAt(1), m.DockerOverview(), "", func() {
		facts(c, tok, fam,
			"ID", shortID(in.ID), m.ColSize(), orDash(m, im.Size),
			m.DockerColCreated(), orDash(m, dockerTime(in.Created)), m.DockerPlatform(), orDash(m, in.Platform()),
			m.DockerLayers(), strconv.Itoa(len(in.RootFS.Layers)), m.DockerUser(), orDash(m, in.Config.User),
			m.DockerBuiltWith(), orDash(m, in.DockerVersion), m.DockerAuthor(), orDash(m, in.Author),
		)
		wideFact(c, tok, fam, m.DockerRepoTags(), orDash(m, strings.Join(in.RepoTags, ", ")))
		wideFact(c, tok, fam, m.DockerDigests(), orDash(m, strings.Join(in.RepoDigests, "\n")))
		wideFact(c, tok, fam, m.DockerEntrypoint(), orDash(m, strings.Join(in.Config.Entrypoint, " ")))
		wideFact(c, tok, fam, m.DockerCommand(), orDash(m, strings.Join(in.Config.Cmd, " ")))
		wideFact(c, tok, fam, m.DockerWorkDir(), orDash(m, in.Config.WorkingDir))
		wideFact(c, tok, fam, m.DockerExposed(), orDash(m, strings.Join(slices.Sorted(maps.Keys(in.Config.ExposedPorts)), ", ")))
		wideFact(c, tok, fam, m.DockerVolumes(), orDash(m, strings.Join(slices.Sorted(maps.Keys(in.Config.Volumes)), ", ")))
	})
}

// imgContainers are the containers made from the image; a right click on
// one opens its menu.
func (a *App) imgContainers(c *ui.Context, m locales.Messages, tok theme.Tokens, tc *conn) {
	cs := a.docker.img.Containers
	a.panelCard(c, tok, "imgctrs", icons.Docker, tok.TintAt(2), m.DockerContainers(strconv.Itoa(len(cs))), "", func() {
		if len(cs) == 0 {
			panelNote(c, tok, m.DockerImageUnused())
			return
		}
		for i, ct := range cs {
			row := ui.Row(c.Key(i)).Gap(8).FillWidth().AlignItems(ui.Center).Children(func() {
				dot(c, stateTint(tok, ct.State))
				ui.Text(c, ct.Name).FontSize(theme.Footnote).FontWeight(500).SingleLine().Shrink(1)
				ui.Spacer(c)
				ui.Text(c, ct.Status).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(0)
			})
			a.contextMenu(row, func() []menuEntry { return a.containerEntries(m, tc, ct.ID) })
		}
	})
}

// imgHistory are the steps that built the image, the last first.
func (a *App) imgHistory(c *ui.Context, m locales.Messages, tok theme.Tokens, fam string) {
	ls := a.docker.img.History
	if len(ls) == 0 {
		return
	}
	a.panelCard(c, tok, "history", icons.History, tok.TintAt(5), m.DockerHistory(), strconv.Itoa(len(ls)), func() {
		for i, l := range ls {
			if i > 0 {
				ui.Box(c).Height(1).FillWidth().Background(tok.Line)
			}
			ui.Row(c.Key(i)).Gap(10).FillWidth().AlignItems(ui.Start).Children(func() {
				ui.Text(c, l.Size).Font(fam).FontSize(theme.Caption).TextColor(tok.TextMuted).Width(64).Shrink(0).SingleLine()
				ui.Text(c, l.Created).FontSize(theme.Caption).TextColor(tok.TextMuted).Width(96).Shrink(0).SingleLine()
				ui.Text(c, l.Step()).Font(fam).FontSize(theme.Caption).Grow(1).Shrink(1).MinWidth(0).Selectable()
			})
		}
	})
}
