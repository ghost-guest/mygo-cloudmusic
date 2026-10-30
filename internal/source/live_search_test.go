package source

import (
	"os"
	"testing"

	"netease-music-mygo/internal/netease"
)

// TestLiveSearch verifies a bundled source's declared search action works.
// Run with: go test -run TestLiveSearch -v ./internal/source/ (SMOKE=1)
func TestLiveSearch(t *testing.T) {
	if os.Getenv("SMOKE") == "" {
		t.Skip("set SMOKE=1")
	}
	m := NewManager(t.TempDir())
	m.EnsureDefaults(nil)
	for _, ls := range m.List() {
		for plat, info := range ls.metaFor().Sources {
			t.Logf("%s: %s actions=%v quals=%v name=%q", ls.Slug, plat, info.Actions, info.Qualitys, info.Name)
		}
	}
	for _, p := range m.SearchPlatforms() {
		got, err := m.Search(p.ID, "晴天", 1, 10)
		if err != nil {
			t.Logf("%s(%s): search error: %v", p.Name, p.ID, err)
			continue
		}
		t.Logf("%s(%s): %d hits, first=%+v", p.Name, p.ID, len(got), firstHit(got))
	}
	// Resolve a wy track end to end.
	st, err := m.ResolveWyURL(netease.Track{ID: 186016, Name: "晴天", Artists: []netease.Artist{{Name: "周杰伦"}}, Album: netease.Album{Name: "叶惠美"}}, "320k")
	if err != nil {
		t.Logf("wy resolve: %v", err)
	} else {
		t.Logf("wy resolve OK: %s (%s)", short2(st.URL), st.Type)
	}
}

func firstHit(s []SourceTrack) SourceTrack {
	if len(s) == 0 {
		return SourceTrack{}
	}
	return s[0]
}

func short2(u string) string {
	if len(u) > 70 {
		return u[:70] + "…"
	}
	return u
}
