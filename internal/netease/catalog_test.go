package netease

import "testing"

// A search of playlists is counted in its total, so a page full of playlists
// is not read as an empty result and shown as "没有找到".
func TestSearchResultCountsPlaylists(t *testing.T) {
	r := SearchResult{Playlists: []Playlist{{ID: 1}, {ID: 2}}, Total: 536}
	if r.Len() != 2 {
		t.Fatalf("Len = %d, want the two playlists", r.Len())
	}
}
