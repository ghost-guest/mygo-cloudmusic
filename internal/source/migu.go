package source

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/guohuiyuan/music-lib/migu"
	"github.com/guohuiyuan/music-lib/model"
)

// Migu (咪咕音乐) is searched and resolved natively through music-lib
// (github.com/guohuiyuan/music-lib, the engine behind go-music-dl). It is the
// one platform there that serves whole songs without a cookie or signing
// service, and it often serves FLAC, so it is the best place to find a song
// that NetEase and Kuwo will not play in full.
//
// music-lib's download needs the whole model.Song (a copyright id travels
// with it), not just an id, so the songs a search returns are kept here until
// they are resolved. A track restored from a saved session has no entry, and
// is found again by its name and singer.

// miguCache holds the songs a search returned, by their migu id. A long
// session of searching would otherwise keep every hit for ever, so it is
// emptied once it grows past miguCacheLimit; a song lost that way is simply
// looked up again.
var miguCache sync.Map // id -> model.Song

const miguCacheLimit = 2048

var miguCacheSize atomic.Int64 // entries in miguCache

// MiguSearch searches 咪咕音乐. page is 1-based like the other platforms; the
// library returns one page at a time and has no offset, so only the first page
// is paged through.
func MiguSearch(keyword string, page, limit int) ([]SourceTrack, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, fmt.Errorf("empty keyword")
	}
	if page > 1 {
		// The library searches the top hits only; a second page has nothing
		// new to add, so it ends the search rather than repeating it.
		return nil, nil
	}
	songs, err := migu.Search(keyword)
	if err != nil {
		return nil, err
	}
	if len(songs) == 0 {
		return nil, fmt.Errorf("没找到")
	}
	if limit > 0 && len(songs) > limit {
		songs = songs[:limit]
	}
	out := make([]SourceTrack, 0, len(songs))
	for _, s := range songs {
		miguRemember(s)
		out = append(out, SourceTrack{
			ID:       s.ID,
			Name:     s.Name,
			Singer:   s.Artist,
			Album:    s.Album,
			Source:   "mg",
			Interval: kuwoInterval(fmt.Sprint(s.Duration)),
			Quality:  miguQuality(s),
		})
	}
	return out, nil
}

// MiguURL resolves a 咪咕 song to its audio. The song is taken from the search
// that found it; one restored from a session is searched for again by its name
// and singer, and matched on its id.
func MiguURL(songID, name, singer string) (string, string, error) {
	s, ok := miguCached(songID)
	if !ok {
		var err error
		s, err = miguFind(songID, name, singer)
		if err != nil {
			return "", "", err
		}
	}
	u, err := migu.GetDownloadURL(&s)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(u) == "" {
		return "", "", fmt.Errorf("migu returned no url")
	}
	ext := s.Ext
	if ext == "" {
		ext = "mp3"
	}
	return u, ext, nil
}

// miguRemember keeps a song for resolving, emptying the cache when it has
// grown past its limit.
func miguRemember(s model.Song) {
	if miguCacheSize.Load() >= miguCacheLimit {
		miguCache.Range(func(k, _ any) bool {
			miguCache.Delete(k)
			return true
		})
		miguCacheSize.Store(0)
	}
	if _, loaded := miguCache.LoadOrStore(s.ID, s); !loaded {
		miguCacheSize.Add(1)
	}
}

func miguCached(id string) (model.Song, bool) {
	v, ok := miguCache.Load(id)
	if !ok {
		return model.Song{}, false
	}
	return v.(model.Song), true
}

// miguFind searches for a song that a saved session only knows by id, name and
// singer, and caches what it finds so resolving it again is free.
func miguFind(id, name, singer string) (model.Song, error) {
	keyword := strings.TrimSpace(name + " " + singer)
	if keyword == "" {
		return model.Song{}, fmt.Errorf("migu: no song to look up")
	}
	songs, err := migu.Search(keyword)
	if err != nil {
		return model.Song{}, err
	}
	for _, s := range songs {
		if s.ID == id {
			miguRemember(s)
			return s, nil
		}
	}
	// The id was not found again (the platform renumbered it): take the hit
	// that is the same song by name and singer.
	for _, s := range songs {
		if normalizeName(s.Name) == normalizeName(name) {
			miguRemember(s)
			return s, nil
		}
	}
	return model.Song{}, fmt.Errorf("migu: song not found")
}

// miguQuality names the quality a hit offers, for the 来源 list.
func miguQuality(s model.Song) string {
	switch s.Ext {
	case "flac":
		return "无损"
	case "mp3":
		if s.Bitrate >= 320 {
			return "320k"
		}
	}
	return ""
}
