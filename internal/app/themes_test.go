package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"netease-music-mygo/internal/netease"
)

// Picking a theme on the home page loads that theme's playlists, so the
// section can recommend 90后流行歌 rather than only whatever loaded first.
func TestThemeChipSwitches(t *testing.T) {
	a := newTestApp()
	a.featured = []netease.Playlist{{ID: 1, Name: "推荐"}}
	a.charts = []netease.Playlist{{ID: 2, Name: "热歌榜"}}
	a.themeName = homeThemes[0]
	tt := ui.NewTester(a.view, 1200, 900)
	tt.Frame()

	if err := tt.Click("90后流行歌"); err != nil {
		t.Fatalf("click theme: %v", err)
	}
	tt.Frame()
	if a.themeName != "90后流行歌" {
		t.Fatalf("theme = %q, want 90后流行歌", a.themeName)
	}
	if !a.themeLoading {
		t.Fatal("the theme is not loading its playlists")
	}
}
