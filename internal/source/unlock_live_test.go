package source

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"netease-music-mygo/internal/netease"
)

// Reports, for VIP/restricted NetEase songs, whether the cross-platform
// fallback produces a full-length playable stream.
func TestVIPUnlockLive(t *testing.T) {
	if os.Getenv("SMOKE") == "" {
		t.Skip("set SMOKE=1")
	}
	queries := []string{"晴天 周杰伦", "青花瓷 周杰伦", "富士山下 陈奕迅", "七里香 周杰伦", "稻香 周杰伦", "演员 薛之谦"}
	m := NewManager(t.TempDir())
	m.EnsureDefaults(nil)
	for _, q := range queries {
		tracks, err := netease.Search(q, 3)
		if err != nil || len(tracks) == 0 {
			t.Logf("%-16s netease search failed: %v", q, err)
			continue
		}
		var tr netease.Track
		for _, c := range tracks {
			if !isFragment(c.Name) {
				tr = c
				break
			}
		}
		if tr.ID == 0 {
			tr = tracks[0]
		}
		start := time.Now()
		st, err := m.ResolveWyURL(tr, "320k")
		el := time.Since(start).Round(time.Millisecond)
		if err != nil {
			t.Logf("%-16s FAIL (%v) %v", tr.Name, el, err)
			continue
		}
		req, _ := http.NewRequest("HEAD", st.URL, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		n := "-"
		if resp, err := http.DefaultClient.Do(req); err == nil {
			n = resp.Header.Get("Content-Length")
			resp.Body.Close()
		}
		host := "kuwo"
		if !strings.Contains(st.URL, "kuwo.cn") {
			host = "other"
		}
		t.Logf("%-16s OK (%v) %-5s %-6s %8s B  %s", tr.Name, el, host, st.Level, n, short2(st.URL))
	}
}
