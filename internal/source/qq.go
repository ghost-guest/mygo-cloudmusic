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

// QQSearch searches QQ Music (QQ音乐) directly, the way the cross-platform
// search dropdown's 来源 choices should actually return playable songs. The
// imported sources' search backends are dead, so the app searches QQ's public
// API itself and resolves the result through QQURL.
func QQSearch(keyword string, page, limit int) ([]SourceTrack, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, fmt.Errorf("empty keyword")
	}
	q := url.Values{}
	q.Set("w", keyword)
	q.Set("format", "json")
	q.Set("n", strconv.Itoa(limit))
	q.Set("p", strconv.Itoa(page))
	api := "https://c.y.qq.com/soso/fcgi-bin/client_search_cp?" + q.Encode()
	req, err := http.NewRequestWithContext(context.Background(), "GET", api, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", "https://y.qq.com/")
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
		Data struct {
			Song struct {
				List []struct {
					Songmid  string `json:"songmid"`
					Songname string `json:"songname"`
					Singer   []struct {
						Name string `json:"name"`
					} `json:"singer"`
					Albumname string `json:"albumname"`
					Interval  int    `json:"interval"`
					Pay       struct {
						Payplay int `json:"payplay"`
					} `json:"pay"`
				} `json:"list"`
			} `json:"song"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("qq search: %w", err)
	}
	out := make([]SourceTrack, 0, len(parsed.Data.Song.List))
	for _, s := range parsed.Data.Song.List {
		if s.Songmid == "" || s.Songname == "" {
			continue
		}
		var singer strings.Builder
		for i, a := range s.Singer {
			if i > 0 {
				singer.WriteString("/")
			}
			singer.WriteString(a.Name)
		}
		out = append(out, SourceTrack{
			ID:        s.Songmid,
			Name:      s.Songname,
			Singer:    singer.String(),
			Album:     s.Albumname,
			Source:    "tx",
			Interval:  qqInterval(s.Interval),
			Paywalled: s.Pay.Payplay != 0,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("没找到")
	}
	return out, nil
}

// qqInterval turns QQ's seconds into mm:ss.
func qqInterval(sec int) string {
	if sec <= 0 {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", sec/60, sec%60)
}

const qqVkeyAPI = "https://u.y.qq.com/cgi-bin/musicu.fcg"

// QQURL resolves a QQ song (its songmid) to a playable URL. The player
// decodes MP3 and FLAC only, so it asks the vkey server for the 128k MP3
// (M500) first; the default purl is an AAC .m4a the player cannot decode, and
// is only used as a last try. QQ's vkey server hands back the purl, which we
// join with a streaming server host.
func QQURL(songmid, level string) (string, error) {
	mid := strings.TrimSpace(songmid)
	if mid == "" {
		return "", fmt.Errorf("qq: empty song mid")
	}
	if u, err := qqVkey(mid, "M500"+mid+".mp3"); err == nil && u != "" {
		return u, nil
	}
	if u, err := qqVkey(mid, "M800"+mid+".mp3"); err == nil && u != "" {
		return u, nil
	}
	if u, err := qqVkey(mid, ""); err == nil && u != "" && !strings.Contains(u, ".m4a") {
		return u, nil
	}
	return "", fmt.Errorf("qq returned no url")
}

// qqVkey asks the vkey server for one file (filename "" uses the default
// purl) and returns the URL it can stream from, or "".
func qqVkey(mid, filename string) (string, error) {
	param := map[string]any{
		"guid":      "10000",
		"songmid":   []string{mid},
		"songtype":  []int{0},
		"uin":       "0",
		"loginflag": 1,
		"platform":  "20",
	}
	if filename != "" {
		param["filename"] = []string{filename}
	}
	reqBody, err := json.Marshal(map[string]any{
		"req_1": map[string]any{
			"module": "vkey.GetVkeyServer",
			"method": "CgiGetVkey",
			"param":  param,
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(context.Background(), "POST", qqVkeyAPI, strings.NewReader(string(reqBody)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Referer", "https://y.qq.com/")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", hostUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Req1 struct {
			Data struct {
				Sip        []string `json:"sip"`
				Midurlinfo []struct {
					Purl string `json:"purl"`
				} `json:"midurlinfo"`
			} `json:"data"`
		} `json:"req_1"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("qq vkey: %w", err)
	}
	if len(parsed.Req1.Data.Midurlinfo) == 0 || parsed.Req1.Data.Midurlinfo[0].Purl == "" {
		return "", fmt.Errorf("qq returned no url")
	}
	sip := "http://aqqmusic.tc.qq.com/"
	if len(parsed.Req1.Data.Sip) > 0 && parsed.Req1.Data.Sip[0] != "" {
		sip = parsed.Req1.Data.Sip[0]
	}
	u := sip + parsed.Req1.Data.Midurlinfo[0].Purl
	if !strings.HasPrefix(u, "http") {
		return "", fmt.Errorf("qq returned no url")
	}
	return u, nil
}
