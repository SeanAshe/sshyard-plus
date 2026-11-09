package app

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Licoy/sshyard/internal/icons"
	"github.com/Licoy/sshyard/internal/theme"
	"github.com/Licoy/sshyard/locales"
	"github.com/egoist/mygo/ui"
)

type aiAttachmentPreview struct {
	file *aiAttach
	open bool
}

func (f *aiAttach) format() string {
	if f.mime != "" {
		_, format, _ := strings.Cut(f.mime, "/")
		return strings.ToUpper(format)
	}
	if ext := filepath.Ext(f.name); ext != "" {
		return strings.ToUpper(strings.TrimPrefix(ext, "."))
	}
	return "TXT"
}

// attachStrip shares chips between the composer and sent messages, with
// removal available only in the composer: each one row tall, a thumbnail
// or the file's icon flush with its start, its name and kind beside it.
func (a *App) attachStrip(c *ui.Context, tok theme.Tokens, m locales.Messages, files []*aiAttach, remove func(i int)) {
	side := theme.Space(10)
	ui.Row(c.Key("attach")).FillWidth().Wrap().Gap(theme.Space(2)).Children(func() {
		for i, f := range files {
			end := theme.Space(3)
			if remove != nil {
				end = theme.Space(1)
			}
			ui.Row(c.Key("at:"+strconv.Itoa(i))).MaxWidth(theme.Space(55)).Shrink(1).MinWidth(0).Height(side).
				Gap(theme.Space(1)).Padding(0, end, 0, 0).AlignItems(ui.Center).Radius(theme.RadiusControl).
				Background(a.contentSurfaceFill(tok.Inset)).Border(1, tok.Line).Clip().Label(f.name).Children(func() {
				var body ui.Element
				if f.bmp != nil {
					body = ui.ButtonBase(c.Key("preview")).Shrink(1).MinWidth(0).Gap(theme.Space(2)).AlignItems(ui.Center).
						Label(m.AiPreviewAttachment(f.name)).Tooltip(m.AiPreviewAttachment(f.name)).Cursor(ui.CursorPointer)
				} else {
					body = ui.Row(c.Key("file")).Shrink(1).MinWidth(0).Gap(theme.Space(2)).AlignItems(ui.Center).Tooltip(f.name)
				}
				body.Children(func() {
					if f.bmp != nil {
						ui.Image(c, f.bmp).Size(side, side).Shrink(0).Fit(ui.Cover)
					} else {
						ui.Box(c).Size(side, side).Shrink(0).Center().Background(tok.Fill).Children(func() {
							ui.Icon(c, f.icon().SVG).Size(theme.Space(4), theme.Space(4)).TextColor(tok.TextMuted)
						})
					}
					ui.Column(c).Shrink(1).MinWidth(0).MaxWidth(theme.Space(35)).Children(func() {
						ui.Text(c, f.name).FontSize(theme.Footnote).FontWeight(500).SingleLine().Shrink(1)
						ui.Text(c, f.format()+" · "+humanSize(int64(len(f.data)))).FontSize(theme.Caption).TextColor(tok.TextMuted).SingleLine().Shrink(1)
					})
				})
				if f.bmp != nil && body.Clicked() {
					a.attachmentPreview = &aiAttachmentPreview{file: f, open: true}
				}
				if remove != nil {
					button := (IconButton{Icon: icons.Close, Label: m.AiRemoveAttachment(f.name), Size: Tiny}).Show(c, "rm").Shrink(0)
					if button.Clicked() {
						remove(i)
					}
				}
			})
		}
	})
}

func (a *App) aiAttachmentPreviewView(c *ui.Context, m locales.Messages, tok theme.Tokens) {
	p := a.attachmentPreview
	if p == nil {
		return
	}
	ww, wh := c.Size()
	w, h := min(float32(960), max(0, ww-theme.Space(24))), min(float32(640), max(0, wh-theme.Space(48)))
	sheet(ui.Modal(c.Key("aiattachmentpreview"), &p.open, func() {
		ui.Column(c).Width(w).Gap(theme.Space(3)).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(theme.Space(2)).Children(func() {
				ui.Text(c, p.file.name).FontSize(theme.Title3).FontWeight(600).SingleLine().Grow(1).Shrink(1).MinWidth(0)
				if (IconButton{Icon: icons.Close, Label: m.ClosePreview()}).Clicked(c, "closeattachpreview") {
					p.open = false
				}
			})
			ui.Image(c, p.file.bmp).Size(w, h).Fit(ui.ScaleDown).Radius(theme.RadiusControl).Background(tok.Canvas).Label(m.AiPreviewAttachment(p.file.name))
		})
	}), tok)
	if !p.open {
		a.attachmentPreview = nil
	}
}
