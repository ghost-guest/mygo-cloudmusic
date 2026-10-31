package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"netease-music-mygo/internal/netease"
)

// MatchTrack finds a NetEase song on another platform by its name and singer
// and returns a playable stream from there, the way UnblockNeteaseMusic
// (github.com/UnblockNeteaseMusic/server) unlocks a grayed-out or VIP song:
// the song is looked up on Kuwo first, then QQ Music, and the best matching
// hit that actually serves audio is used. The platforms' own search and
// resolve are native (see kuwo.go and qq.go), so this needs no imported
// source and stays fast.
func (m *SourceManager) MatchTrack(t netease.Track, level string) (*netease.Stream, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return nil, fmt.Errorf("match: song has no name")
	}
	singer := strings.TrimSpace(t.ArtistNames())
	keyword := strings.TrimSpace(name + " " + singer)
	var lastErr error
	want := time.Duration(t.Duration) * time.Millisecond
	for _, match := range []func(string, string, string, time.Duration) (*netease.Stream, error){
		m.matchKuwo,
		m.matchQQ,
	} {
		st, err := match(name, singer, level, want)
		if err != nil {
			lastErr = err
			continue
		}
		return st, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("match: no hit for %q", keyword)
	}
	return nil, lastErr
}

// matchKuwo searches Kuwo for the song and returns the first hit whose audio
// really is the whole song. Kuwo's search mixes in live takes, DJ edits and
// 30-second previews, and its resolver happily returns those, so a hit is only
// accepted once its file is long enough to be the song rather than a clip.
func (m *SourceManager) matchKuwo(name, singer, level string, want time.Duration) (*netease.Stream, error) {
	keyword := strings.TrimSpace(name + " " + singer)
	hits, err := KuwoSearch(keyword, 1, 10)
	if err != nil {
		return nil, err
	}
	hits = rankHits(name, singer, hits)
	if len(hits) == 0 {
		return nil, fmt.Errorf("kuwo: no close match")
	}
	var lastErr error
	for _, h := range hits[0:min(len(hits), 3)] {
		u, err := KuwoURL(h.ID, level)
		if err != nil {
			lastErr = err
			continue
		}
		if err := m.probeStream(u); err != nil {
			lastErr = err
			continue
		}
		if !audioFits(u, want) {
			lastErr = fmt.Errorf("kuwo: %q is a short clip", h.Name)
			continue
		}
		return &netease.Stream{URL: u, Level: level, Type: inferType(u)}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("kuwo: no usable hit")
	}
	return nil, lastErr
}

// matchQQ searches QQ Music for the song and returns the first free hit whose
// audio really is the whole song, with the same clip check as Kuwo.
func (m *SourceManager) matchQQ(name, singer, level string, want time.Duration) (*netease.Stream, error) {
	keyword := strings.TrimSpace(name + " " + singer)
	hits, err := QQSearch(keyword, 1, 10)
	if err != nil {
		return nil, err
	}
	hits = rankHits(name, singer, hits)
	if len(hits) == 0 {
		return nil, fmt.Errorf("qq: no close match")
	}
	var lastErr error
	for _, h := range hits[0:min(len(hits), 3)] {
		if h.Paywalled {
			lastErr = fmt.Errorf("qq: %q is paywalled", h.Name)
			continue
		}
		u, err := QQURL(h.ID, level)
		if err != nil {
			lastErr = err
			continue
		}
		if err := m.probeStream(u); err != nil {
			lastErr = err
			continue
		}
		if !audioFits(u, want) {
			lastErr = fmt.Errorf("qq: %q is a short clip", h.Name)
			continue
		}
		return &netease.Stream{URL: u, Level: level, Type: inferType(u)}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("qq: no usable hit")
	}
	return nil, lastErr
}

// rankHits orders the hits so the actual full song comes first, and drops
// previews, live takes, DJ edits and backing tracks. Kuwo/QQ search returns
// many near-matches and the first is often an "(Live)" or "42秒片段" entry,
// which resolves to a short clip rather than the song. An exact name+singer
// hit wins; then an exact name with any singer (compilations, instrumentals);
// then a hit whose name starts with the song's. A merely similar name never
// qualifies.
func rankHits(name, singer string, hits []SourceTrack) []SourceTrack {
	wantName, wantSinger := normalizeName(name), normalizeName(singer)
	if wantName == "" {
		return nil
	}
	var sameName, exactName, prefix []SourceTrack
	for _, h := range hits {
		if isFragment(h.Name) {
			continue
		}
		hn, hs := normalizeName(h.Name), normalizeName(h.Singer)
		switch {
		case hn == wantName && (wantSinger == "" || strings.Contains(hs, wantSinger) || strings.Contains(wantSinger, hs)):
			sameName = append(sameName, h)
		case hn == wantName:
			exactName = append(exactName, h)
		case strings.HasPrefix(hn, wantName) && len(hn) <= len(wantName)+3:
			prefix = append(prefix, h)
		}
	}
	out := append(sameName, exactName...)
	return append(out, prefix...)
}

// isFragment reports whether a track title looks like a preview, remix, live
// take, or backing track rather than the studio song. Those resolve to short
// clips on Kuwo and must not be picked for an unlock.
func isFragment(title string) bool {
	lower := strings.ToLower(title)
	for _, bad := range []string{
		"片段", "试听", "预览", "伴奏", "消音", "dj", "remix", "混音",
		"live", "现场", "演唱会", "铃声", "纯音乐", "inst.",
		"instrumental", "karaoke", "翻唱", "cover", "串烧", "网络歌曲",
		"片段版", "秒片段", "伴奏版", "消音版", "吉他版", "钢琴版", "八音盒",
	} {
		if strings.Contains(lower, bad) {
			return true
		}
	}
	return false
}

// audioFits reports whether the file at u is long enough to be the song. Kuwo
// answers an 85KB trial clip ("bitrate$1") for songs it will not stream in
// full, so a resolved URL is measured before it is offered. A floor of about
// 64kbps rejects those previews while accepting a full low-bitrate encode.
// An unknown song length (0) skips the check.
func audioFits(u string, want time.Duration) bool {
	if want <= 0 {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return true // verification failed to run; do not reject the song
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("User-Agent", hostUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 2))
	size := contentTotal(resp.Header)
	if size <= 0 {
		return true
	}
	floor := int64(want.Seconds() * 8000)
	return size >= floor
}

// contentTotal reads the whole size from a ranged response: Content-Range
// ("bytes 0-0/10792943"), or Content-Length when the server ignored the range.
func contentTotal(h http.Header) int64 {
	if cr := h.Get("Content-Range"); cr != "" {
		if i := strings.LastIndex(cr, "/"); i >= 0 {
			if n, err := strconv.ParseInt(cr[i+1:], 10, 64); err == nil {
				return n
			}
		}
	}
	if n, err := strconv.ParseInt(h.Get("Content-Length"), 10, 64); err == nil {
		return n
	}
	return 0
}

// normalizeName lowercases and drops everything but letters and digits, so
// "晴天 (Live)" and "晴天" compare alike across platforms.
func normalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
