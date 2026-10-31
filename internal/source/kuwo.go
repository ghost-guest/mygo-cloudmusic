package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// KuwoSearch searches Kuwo (酷我) directly. The bundled sources once offered
// cross-platform search, but their search backends are now dead (502/404),
// so the app searches Kuwo's public API itself and resolves the result
// through KuwoURL. This is why the search "来源" dropdown can offer 酷我 even
// though no imported source declares a search action.
func KuwoSearch(keyword string, page, limit int) ([]SourceTrack, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, fmt.Errorf("empty keyword")
	}
	q := url.Values{}
	q.Set("client", "kt")
	q.Set("all", keyword)
	q.Set("pn", strconv.Itoa(page-1))
	q.Set("rn", strconv.Itoa(limit))
	q.Set("uid", "794762570")
	q.Set("ver", "kwplayer_ar_9.2.2.1")
	q.Set("vipver", "1")
	q.Set("show_copyright_off", "1")
	q.Set("newver", "1")
	q.Set("ft", "music")
	q.Set("cluster", "0")
	q.Set("strategy", "2012")
	q.Set("encoding", "utf8")
	q.Set("rformat", "json")
	q.Set("mobi", "1")
	api := "http://search.kuwo.cn/r.s?" + q.Encode()

	req, err := http.NewRequestWithContext(context.Background(), "GET", api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hostUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Abslist []struct {
			MusicRID string `json:"MUSICRID"`
			SongName string `json:"SONGNAME"`
			Artist   string `json:"ARTIST"`
			Album    string `json:"ALBUM"`
			SongTime string `json:"SONGTIME"`
			Format   string `json:"MUSICFORMAT"`
		} `json:"abslist"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("kuwo search: %w", err)
	}
	out := make([]SourceTrack, 0, len(parsed.Abslist))
	for _, a := range parsed.Abslist {
		if a.MusicRID == "" || a.SongName == "" {
			continue
		}
		out = append(out, SourceTrack{
			ID:       a.MusicRID,
			Name:     a.SongName,
			Singer:   a.Artist,
			Album:    a.Album,
			Source:   "kw",
			Interval: kuwoInterval(a.SongTime),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("没找到")
	}
	return out, nil
}

// kuwoInterval turns Kuwo's SONGTIME (seconds, sometimes empty) into mm:ss.
func kuwoInterval(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		m := n / 60
		sec := n % 60
		return fmt.Sprintf("%02d:%02d", m, sec)
	}
	return s
}

// KuwoURL resolves a Kuwo song (its MUSICRID, e.g. "MUSIC_228908") to a
// playable MP3 URL.
//
// It goes through Kuwo's signed mobile endpoint. The old public
// `antiserver.kuwo.cn/anti.s?type=convert_url` route still answers with a
// URL, but it now ignores the rid: different songs came back pointing at the
// same file, so a greyed-out NetEase song resolved to unrelated audio. The
// mobile endpoint honours the rid and returns that song's own bitrate.
func KuwoURL(songID string, level string) (string, error) {
	rid := strings.TrimSpace(songID)
	rid = strings.TrimPrefix(rid, "MUSIC_")
	rid = strings.TrimPrefix(rid, "music_")
	if rid == "" {
		return "", fmt.Errorf("kuwo: empty song id")
	}
	br := kuwoBitrate(level)
	q := url.Values{}
	q.Set("f", "web")
	q.Set("user", "0")
	q.Set("source", kuwoMobileSource)
	q.Set("type", "convert_url_with_sign")
	q.Set("rid", rid)
	q.Set("br", br)
	api := "http://nmobi.kuwo.cn/mobi.s?" + q.Encode()

	req, err := http.NewRequestWithContext(context.Background(), "GET", api, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", kuwoMobileUA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			URL    string `json:"url"`
			Format string `json:"format"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("kuwo: %w", err)
	}
	u := strings.TrimSpace(parsed.Data.URL)
	if u == "" || u == "None" || !strings.HasPrefix(u, "http") {
		if parsed.Msg != "" {
			return "", fmt.Errorf("kuwo: %s", parsed.Msg)
		}
		return "", fmt.Errorf("kuwo returned no url")
	}
	return u, nil
}

const (
	kuwoMobileSource = "kwplayerhd_ar_1.1.4.5_tianbao_T1A_qirui.apk"
	kuwoMobileUA     = "Mozilla/5.0 (Linux; Android) AppleWebKit/537.36"
)

// kuwoBitrate maps a quality level onto the mobile endpoint's br codes.
func kuwoBitrate(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "128k", "128":
		return "128kmp3"
	case "flac", "lossless":
		return "2000kflac"
	case "flac24bit", "hires":
		return "4000kflac"
	default:
		return "320kmp3"
	}
}
