package app

import (
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"netease-music-mygo/internal/source"
)

// importDialog asks for a music source link, then imports it.
type importDialog struct {
	open bool
	url  string
	busy bool
	err  string
}

// musicSourceRows builds the "音乐源" settings group's rows.
func (a *app) musicSourceRows(c *ui.Context, row func(string, string, func())) {
	noControl := func() {}
	if a.sources == nil {
		row("音乐源", "未初始化", noControl)
		return
	}
	list := a.sources.List()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	for _, ls := range list {
		desc := sourceStatus(ls)
		row(ls.Name, desc, func() {
			on := ls.Enabled
			if ui.Switch(c.Key("src-"+ls.Slug), &on).Label("启用").Changed() {
				if err := a.sources.SetEnabled(ls.Slug, on); err != nil {
					a.importing.err = err.Error()
				}
				a.redraw()
			}
			if ui.Button(c.Key("reload-"+ls.Slug), "重载").Label("重载音乐源").Clicked() {
				_ = a.sources.Reload(ls.Slug)
				a.redraw()
			}
			if ui.Button(c.Key("remove-"+ls.Slug), "移除").Label("移除音乐源").Clicked() {
				_ = a.sources.Remove(ls.Slug)
				// Clearing the last source opts out of the defaults, so
				// they do not come back on the next launch.
				if len(a.sources.List()) == 0 {
					a.sources.DisableDefaults()
				}
				a.redraw()
			}
		})
	}
	row("导入音乐源…", "从链接导入 LX 音乐源（.js），用于播放 VIP 与灰色歌曲", func() {
		if ui.Button(c.Key("import-source"), "导入").Label("导入音乐源").Clicked() {
			a.importing.open = true
			a.importing.err = ""
		}
	})
}

// sourceStatus describes what a source is doing, for its settings row.
func sourceStatus(ls source.LoadedSource) string {
	if ls.Error != "" {
		return "加载失败：" + ls.Error
	}
	if !ls.Enabled {
		return "已停用"
	}
	if ls.Author != "" || ls.Version != "" {
		return joinNonEmpty(ls.Author, ls.Version, " · ")
	}
	return "已启用"
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// importSourceDialog is the modal that asks for a source link.
func (a *app) importSourceDialog(c *ui.Context) {
	p := a.pal
	d := &a.importing
	ui.DialogBase(c, &d.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.35))
		panel.Width(480).Padding(24).Radius(18).Gap(14).
			Background(p.bg).Border(1, p.hairline).Shadow(0, 24, 60, -12, ui.RGBA(0, 0, 0, 0.4)).
			Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.94}, Exit: &ui.Motion{Scale: 0.96}})
		ui.Text(c, "导入音乐源").FontSize(17).FontWeight(700)
		ui.Text(c, "粘贴一个 LX 音乐源的链接（.js 文件）。导入后，当网易云无法播放的歌曲将尝试用该源解析直链。").FontSize(12).TextColor(p.muted).LineHeight(1.4)
		in := ui.TextInput(c.Key("source-url"), &d.url).Placeholder("https://raw.githubusercontent.com/…/latest.js").Label("音乐源链接").FontSize(13).AutoFocus()
		if in.Submitted() && !d.busy {
			a.submitSource()
		}
		if d.err != "" {
			ui.Text(c, d.err).FontSize(12).TextColor(ui.RGBA(255, 100, 100, 0.95)).LineHeight(1.4)
		}
		ui.Row(c).Gap(10).Justify(ui.End).Children(func() {
			if pillButton(c.Key("src-cancel"), nil, "取消", p.hover.Over(p.bg), p.text).Clicked() {
				d.open = false
				d.err = ""
			}
			action := "导入"
			if d.busy {
				action = "正在导入…"
			}
			ok := pillButton(c.Key("src-ok"), nil, action, p.accent, p.onAccent)
			if strings.TrimSpace(d.url) == "" || d.busy {
				ok.Disabled(true).Opacity(0.5)
			}
			if ok.Clicked() {
				a.submitSource()
			}
		})
	})
}

// submitSource imports the link in the dialog.
func (a *app) submitSource() {
	d := &a.importing
	url := strings.TrimSpace(d.url)
	if url == "" || d.busy {
		return
	}
	d.busy = true
	d.err = ""
	a.redraw()
	go func() {
		_, err := a.sources.Add(url)
		a.post(func() {
			d.busy = false
			if err != nil {
				d.err = "导入失败：" + err.Error()
				return
			}
			d.open = false
			d.url = ""
			a.importing.err = ""
		})
	}()
}
