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

// A next page of playlists joins the ones found before it, and its count
// does not lower theirs, as NetEase's does between the pages of one search:
// the playlists used to be dropped, which emptied the grid, sent the search
// back to its first page, and made the count flip between two numbers.
func TestSearchResultAppendKeepsPlaylists(t *testing.T) {
	r := SearchResult{Playlists: []Playlist{{ID: 1}, {ID: 2}}, Total: 681}
	r.Append(SearchResult{Playlists: []Playlist{{ID: 3}}, Total: 277})
	if r.Len() != 3 || r.Playlists[2].ID != 3 {
		t.Fatalf("Len = %d, playlists %v", r.Len(), r.Playlists)
	}
	if r.Total != 681 {
		t.Errorf("Total = %d, want the 681 found at first", r.Total)
	}
}
