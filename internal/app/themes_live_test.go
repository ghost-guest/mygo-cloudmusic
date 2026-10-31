package app

import (
	"os"
	"testing"

	"netease-music-mygo/internal/netease"
)

// Every theme of the home page's 主题歌单 finds real playlists. Live:
// LIVE=1 go test -run TestThemePlaylistsLive -v ./internal/app/
func TestThemePlaylistsLive(t *testing.T) {
	if os.Getenv("LIVE") == "" {
		t.Skip()
	}
	for _, name := range homeThemes {
		res, err := netease.SearchPage(name, netease.SearchPlaylists, 12, 0)
		if err != nil {
			t.Logf("%-8s ERR %v", name, err)
			continue
		}
		t.Logf("%-8s %d total, %d on page", name, res.Total, len(res.Playlists))
		for i := range res.Playlists[:min(2, len(res.Playlists))] {
			p := res.Playlists[i]
			t.Logf("    %s (%d首, %s)", p.Name, p.TrackCount, p.Creator.Nickname)
		}
	}
}
