package source

import (
	"net/http"
	"os"
	"testing"
	"time"

	"netease-music-mygo/internal/netease"
)

// 咪咕音乐 is searched and resolved through music-lib, and serves a whole
// song (often FLAC) with no cookie, which is what unlocks songs Kuwo only
// previews. Live: go test -run TestMiguLive -v ./internal/source/ (SMOKE=1)
func TestMiguLive(t *testing.T) {
	if os.Getenv("SMOKE") == "" {
		t.Skip("set SMOKE=1")
	}
	hits, err := MiguSearch("晴天 周杰伦", 1, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, h := range hits {
		t.Logf("hit %s/%s %s id=%s", h.Name, h.Singer, h.Interval, h.ID)
	}
	u, ext, err := MiguURL(hits[0].ID, hits[0].Name, hits[0].Singer)
	if err != nil {
		t.Fatalf("url: %v", err)
	}
	req, _ := http.NewRequest("HEAD", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	n, ct := "-", "-"
	if resp, e := http.DefaultClient.Do(req); e == nil {
		n = resp.Header.Get("Content-Length")
		ct = resp.Header.Get("Content-Type")
		resp.Body.Close()
	}
	t.Logf("url ext=%s size=%s ct=%s", ext, n, ct)

	m := NewManager(t.TempDir())
	m.EnsureDefaults(nil)
	for _, q := range []string{"晴天 周杰伦", "富士山下 陈奕迅", "七里香 周杰伦"} {
		tracks, _ := netease.Search(q, 1)
		if len(tracks) == 0 {
			continue
		}
		start := time.Now()
		st, err := m.ResolveWyURL(tracks[0], "exhigh")
		el := time.Since(start).Round(time.Millisecond)
		if err != nil {
			t.Logf("%-12s FAIL %v %v", tracks[0].Name, el, err)
			continue
		}
		t.Logf("%-12s OK %v level=%s type=%s host=%.40s", tracks[0].Name, el, st.Level, st.Type, st.URL)
	}
}
