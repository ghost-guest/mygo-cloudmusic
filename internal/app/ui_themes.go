package app

import (
	"github.com/egoist/mygo/ui"

	"netease-music-mygo/internal/netease"
)

// homeThemes are the themes of the playlists recommended on the home page.
// Each is a search of NetEase's playlists by that name, so a theme needs no
// maintenance: whatever the platform's editors have made for it, and whatever
// listeners have published, comes back ranked.
var homeThemes = []string{
	"90后经典老歌",
	"90后流行歌",
	"千禧年代金曲",
	"粤语经典老歌",
	"华语怀旧金曲",
	"民谣",
}

// loadTheme loads the playlists a theme is about, replacing what the home
// page shows for the theme before.
func (a *app) loadTheme(name string) {
	if name == "" {
		name = homeThemes[0]
	}
	if a.themeLoading && a.themeName == name {
		return
	}
	a.themeName, a.themeLoading, a.themeErr = name, true, ""
	go func() {
		res, err := searchResults(name, netease.SearchPlaylists, 12, 0)
		a.post(func() {
			if a.themeName != name {
				// Another theme was picked while this one loaded: its
				// playlists are not what is asked for now.
				return
			}
			a.themeLoading = false
			if err != nil {
				a.themeErr = "歌单加载失败，请稍后再试"
				return
			}
			a.themePlaylists = res.Playlists
			if len(a.themePlaylists) == 0 {
				a.themeErr = "这个主题暂时没有歌单"
			}
		})
	}()
}

// themeSection is the home page's "主题歌单": the themes to pick from, and the
// playlists the chosen one is about. A song list is not what a theme is
// about, so a theme opens playlists, and each one opens as any playlist does.
func (a *app) themeSection(c *ui.Context) {
	a.section(c, "主题歌单")
	at := 0
	for i, name := range homeThemes {
		if name == a.themeName {
			at = i
		}
	}
	ui.Row(c).Margin(0, 0, 18, 0).Children(func() {
		if segmented(c.Key("theme"), &at, homeThemes...) {
			a.loadTheme(homeThemes[at])
		}
	})
	switch {
	case len(a.themePlaylists) > 0:
		n := min(len(a.themePlaylists), a.columns(c, 168)*2)
		ui.Grid(c).Columns(a.columns(c, 168)).GapX(20).GapY(28).Children(func() {
			for i := range a.themePlaylists[:n] {
				a.playlistCard(c.Key(a.themePlaylists[i].ID), &a.themePlaylists[i])
			}
		})
	case a.themeLoading:
		a.playlistCardsSkeleton(c, a.pageShimmer(c), a.columns(c, 168), 168, false)
	case a.themeErr != "":
		a.empty(c, a.themeErr)
	}
	// Nothing loaded and nothing asked for yet shows the themes alone: a
	// skeleton would shimmer for ever on a page that never asked.
}
