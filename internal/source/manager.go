package source

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"netease-music-mygo/internal/netease"
)

//go:embed defaults/*.js
var bundledFS embed.FS

// SourceManager holds the imported sources, persists them, and answers
// stream and search requests by dispatching to the right source.
type SourceManager struct {
	dir string
	mu  sync.RWMutex
	// sources maps a slug to its loaded state.
	sources map[string]*LoadedSource
	// order keeps the import order, so the first source imported is tried
	// first: map order is not stable, and the priority matters.
	order     map[string]int
	nextOrder int
	// probeURL, when set, stands in for the network probe that confirms a
	// resolved URL serves audio. Tests swap it so no real request is made.
	probeURL func(url string) error
	// noNativeMatch turns off the native cross-platform match (Kuwo, then
	// QQ), so a test of the imported-source path makes no external request.
	noNativeMatch bool
}

// LoadedSource is one imported source and whether it is in use.
type LoadedSource struct {
	Slug    string `json:"slug"`
	URL     string `json:"url"`
	File    string `json:"file"`
	Name    string `json:"name"`
	Author  string `json:"author"`
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
	// Error is non-empty when the script failed to load or register.
	Error string `json:"error,omitempty"`

	meta   SourceMeta
	source *Source
}

// NewManager returns a manager that keeps scripts under dir.
func NewManager(dir string) *SourceManager {
	return &SourceManager{
		dir:     dir,
		sources: map[string]*LoadedSource{},
		order:   map[string]int{},
	}
}

// DefaultSources are the music sources bundled with the app: the ones that
// resolve NetEase (wy) streams for greyed-out or VIP songs, tried in order
// until one returns a playable link. They are embedded so they need no
// network on first launch; the user can still disable, reorder, or remove
// them, and a removed one stays removed. Order is the fallback priority,
// best first — verified to serve audio for VIP songs at build time.
var DefaultSources = []string{
	"helloworld",
	"hyw",
	"fish",
	"qdy",
}

// disableDefaultsKey marks that the user has opted out of default sources,
// so EnsureDefaults stays a no-op even after the source list is cleared.
const disableDefaultsKey = "disable-default-sources"

// seededDefaultsKey lists the defaults already seeded, so a default the user
// removed stays removed across launches while one bundled later is added.
const seededDefaultsKey = "seeded-defaults"

// DisableDefaults stops EnsureDefaults from ever preconfiguring sources.
func (m *SourceManager) DisableDefaults() {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = os.MkdirAll(m.dir, 0o700)
	_ = os.WriteFile(filepath.Join(m.dir, disableDefaultsKey), []byte("1"), 0o600)
}

func (m *SourceManager) defaultsDisabledLocked() bool {
	_, err := os.Stat(filepath.Join(m.dir, disableDefaultsKey))
	return err == nil
}

// readSeeded returns the slugs of the defaults already seeded.
func (m *SourceManager) readSeeded() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(m.dir, seededDefaultsKey))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		if slug := strings.TrimSpace(line); slug != "" {
			out[slug] = true
		}
	}
	return out
}

// writeSeeded records the slugs of the defaults seeded so far.
func (m *SourceManager) writeSeeded(seeded map[string]bool) {
	var b strings.Builder
	for _, slug := range DefaultSources {
		if seeded[slug] {
			b.WriteString(slug + "\n")
		}
	}
	_ = os.MkdirAll(m.dir, 0o700)
	_ = os.WriteFile(filepath.Join(m.dir, seededDefaultsKey), []byte(b.String()), 0o600)
}

// EnsureDefaults writes any bundled source not yet present, so the app ships
// with working sources on first launch and after an update that adds one.
// The scripts are embedded, so this needs no network. It leaves the user's
// list alone otherwise: a default that is already there is kept as is, and
// one the user removed stays removed (its slug is remembered in seeded).
// Clearing every source opts out entirely. Failures are logged, never fatal.
func (m *SourceManager) EnsureDefaults(logf func(string)) {
	m.mu.Lock()
	if m.defaultsDisabledLocked() {
		m.mu.Unlock()
		return
	}
	seeded := m.readSeeded()
	m.mu.Unlock()

	changed := false
	for _, slug := range DefaultSources {
		m.mu.RLock()
		_, present := m.sources[slug]
		m.mu.RUnlock()
		if present {
			// A default already there (seeded before, or imported by the
			// user) is left alone; record it so a future removal sticks.
			if !seeded[slug] {
				seeded[slug] = true
				changed = true
			}
			continue
		}
		if seeded[slug] {
			// Seeded before and now gone: the user removed it, keep it gone.
			continue
		}
		js, err := bundledFS.ReadFile("defaults/" + slug + ".js")
		if err != nil {
			if logf != nil {
				logf("音乐源读取失败 " + slug + "：" + err.Error())
			}
			continue
		}
		if _, err := m.addScript(slug, "bundled:"+slug, js); err != nil {
			if logf != nil {
				logf("音乐源导入失败 " + slug + "：" + err.Error())
			}
			continue
		}
		seeded[slug] = true
		changed = true
		if logf != nil {
			if ls := m.lookup(slug); ls != nil {
				logf("已导入音乐源 " + ls.Name)
			}
		}
	}
	if changed {
		m.writeSeeded(seeded)
	}
}

// HasWy reports whether a NetEase song without a stream of its own (a
// grayed-out VIP one) may still play. The built-in cross-platform matcher
// (Kuwo, then QQ) is always compiled in, so this is true even with no
// imported source; enabling a wy source adds to it. A song the matcher cannot
// find then fails to load and is skipped, rather than the whole list greying.
func (m *SourceManager) HasWy() bool {
	return true
}

// lookup returns a source snapshot by slug with the lock held.
func (m *SourceManager) lookup(slug string) *LoadedSource {
	if ls, ok := m.sources[slug]; ok {
		cp := *ls
		return &cp
	}
	return nil
}

// Dir returns the directory scripts are stored in.
func (m *SourceManager) Dir() string { return m.dir }

// metaFile is where the source list is kept beside the scripts.
func (m *SourceManager) metaFile() string { return filepath.Join(m.dir, "sources.json") }

// LoadAll reads sources.json and starts each enabled source.
func (m *SourceManager) LoadAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, err := os.ReadFile(m.metaFile())
	if err != nil {
		return
	}
	var list []LoadedSource
	if json.Unmarshal(b, &list) != nil {
		return
	}
	for i := range list {
		ls := list[i]
		ls.source = nil
		ls.meta = SourceMeta{Sources: map[string]PlatformInfo{}}
		m.sources[ls.Slug] = &ls
		// The file is written in import order, so its order is the priority.
		if _, ok := m.order[ls.Slug]; !ok {
			m.order[ls.Slug] = m.nextOrder
			m.nextOrder++
		}
		// Always retry an enabled source at launch, whatever a past run
		// recorded; a source may have failed only while its API was down.
		if ls.Enabled {
			ls.Error = ""
			m.startLocked(&ls)
		}
	}
}

// persist writes the source list. Caller holds the lock.
func (m *SourceManager) persist() {
	b, err := json.MarshalIndent(m.listLocked(), "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(m.dir, 0o700)
	_ = os.WriteFile(m.metaFile(), b, 0o600)
}

func (m *SourceManager) listLocked() []LoadedSource {
	out := make([]LoadedSource, 0, len(m.sources))
	for _, ls := range m.sources {
		out = append(out, *ls)
	}
	sort.Slice(out, func(i, j int) bool { return m.order[out[i].Slug] < m.order[out[j].Slug] })
	return out
}

// startLocked loads and starts a source from its file. Caller holds the
// lock. It never returns an error: failures are recorded on the source.
func (m *SourceManager) startLocked(ls *LoadedSource) {
	js, err := os.ReadFile(ls.File)
	if err != nil {
		ls.Error = err.Error()
		ls.source = nil
		return
	}
	header := parseHeader(string(js))
	s, err := runSource(string(js), header)
	if err != nil {
		ls.Error = err.Error()
		ls.source = nil
		return
	}
	ls.Error = ""
	ls.Name = firstNonEmpty(s.Meta().Name, ls.Name)
	ls.Author = firstNonEmpty(s.Meta().Author, ls.Author)
	ls.Version = firstNonEmpty(s.Meta().Version, ls.Version)
	ls.meta = s.Meta()
	ls.source = s
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Add imports a source from url, saves it, and starts it. It returns the
// slug on success.
func (m *SourceManager) Add(url string) (string, error) {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return "", fmt.Errorf("source url must be http(s): %q", url)
	}
	slug := slugOf(url)
	js, err := loadFromURL(context.Background(), url)
	if err != nil {
		return "", fmt.Errorf("fetch source: %w", err)
	}
	return m.addScript(slug, url, js)
}

// addScript saves js as slug's script, starts it, and persists. slug is the
// stable key (a URL hash, or the bundled source name); origin is where it
// came from, kept for display and re-fetch.
func (m *SourceManager) addScript(slug, origin string, js []byte) (string, error) {
	path, err := saveScript(m.dir, slug, js)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ls := &LoadedSource{
		Slug:    slug,
		URL:     origin,
		File:    path,
		Enabled: true,
	}
	m.sources[slug] = ls
	if _, ok := m.order[slug]; !ok {
		m.order[slug] = m.nextOrder
		m.nextOrder++
	}
	m.startLocked(ls)
	m.persist()
	return slug, nil
}

// Remove stops and deletes a source by slug.
func (m *SourceManager) Remove(slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, ok := m.sources[slug]
	if !ok {
		return fmt.Errorf("no such source: %s", slug)
	}
	if ls.source != nil {
		ls.source.Close()
	}
	_ = os.Remove(ls.File)
	delete(m.sources, slug)
	delete(m.order, slug)
	m.persist()
	return nil
}

// SetEnabled turns a source on or off, restarting it as needed.
func (m *SourceManager) SetEnabled(slug string, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, ok := m.sources[slug]
	if !ok {
		return fmt.Errorf("no such source: %s", slug)
	}
	ls.Enabled = enabled
	if enabled {
		if ls.source == nil {
			m.startLocked(ls)
		}
	} else if ls.source != nil {
		ls.source.Close()
		ls.source = nil
	}
	m.persist()
	return nil
}

// Reload reloads a source's script from disk.
func (m *SourceManager) Reload(slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ls, ok := m.sources[slug]
	if !ok {
		return fmt.Errorf("no such source: %s", slug)
	}
	if ls.source != nil {
		ls.source.Close()
		ls.source = nil
	}
	if ls.Enabled {
		m.startLocked(ls)
	}
	m.persist()
	return nil
}

// List returns a snapshot of the imported sources, in import order.
func (m *SourceManager) List() []LoadedSource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]LoadedSource, 0, len(m.sources))
	for _, ls := range m.sources {
		out = append(out, *ls)
	}
	sort.Slice(out, func(i, j int) bool { return m.order[out[i].Slug] < m.order[out[j].Slug] })
	return out
}

// metaFor returns the live meta of a running source, or the stored one.
func (ls *LoadedSource) metaFor() SourceMeta {
	if ls.source != nil {
		return ls.source.Meta()
	}
	return ls.meta
}

// sourcesFor returns running, enabled sources that serve platform with
// action, in import order, best first.
func (m *SourceManager) sourcesFor(platform, action string) []*Source {
	m.mu.RLock()
	type candidate struct {
		order int
		src   *Source
	}
	var cands []candidate
	for slug, ls := range m.sources {
		if !ls.Enabled || ls.source == nil || ls.Error != "" {
			continue
		}
		if info, ok := ls.metaFor().Sources[platform]; ok && info.Supports(action) {
			cands = append(cands, candidate{m.order[slug], ls.source})
		}
	}
	m.mu.RUnlock()
	sort.Slice(cands, func(i, j int) bool { return cands[i].order < cands[j].order })
	out := make([]*Source, len(cands))
	for i, c := range cands {
		out[i] = c.src
	}
	return out
}

// ResolveWyURL returns a stream for a NetEase track from an enabled wy
// source, used as a fallback when NetEase itself has none. It tries each
// wy source in turn, and each source at the best quality it offers for the
// requested NetEase level, so a slow or dead source does not lose the song.
// A resolved URL is probed before being returned: sources sometimes hand
// back a 404 page instead of a link, and we must not trade a missing song
// for a broken one.
// ResolveWyURL returns a stream for a NetEase track that NetEase itself will
// not serve (a grayed-out VIP song). It first matches the song on another
// platform natively (Kuwo, then QQ) as UnblockNeteaseMusic does, then tries
// the imported LX sources, then NetEase's own outer link. Each resolved URL is
// probed before it is returned, so a 404 page is never taken for audio.
func (m *SourceManager) ResolveWyURL(t netease.Track, level string) (*netease.Stream, error) {
	// Native cross-platform match first: fast and needs no imported source.
	if !m.noNativeMatch {
		if st, err := m.MatchTrack(t, level); err == nil {
			return st, nil
		}
	}
	songmid := strconv.FormatInt(t.ID, 10)
	musicInfo := map[string]any{
		"id":        t.ID,
		"name":      t.Name,
		"singer":    t.ArtistNames(),
		"albumName": t.Album.Name,
		"source":    "wy",
		"songmid":   songmid,
		"albumId":   t.Album.ID,
	}
	var lastErr error
	// Try each source in import order, and within a source, the asked quality
	// then lower ones: a VIP song an imported source can serve may only be
	// there at 128k, and one broken quality must not lose the song.
	for _, src := range m.sourcesFor("wy", "musicUrl") {
		quals := wyQualityOrder(level, src.qualitiesFor("wy"))
		for _, q := range quals {
			v, err := src.Request("musicUrl", "wy", map[string]any{"musicInfo": musicInfo, "type": q})
			if err != nil {
				lastErr = err
				continue
			}
			urlStr, ok := v.(string)
			if !ok || strings.TrimSpace(urlStr) == "" {
				lastErr = fmt.Errorf("wy source returned no url")
				continue
			}
			urlStr = strings.TrimSpace(urlStr)
			if err := m.probeStream(urlStr); err != nil {
				lastErr = err
				continue
			}
			return &netease.Stream{URL: urlStr, Level: level, Type: inferType(urlStr)}, nil
		}
	}
	// Last resort: NetEase's own web-player link. It serves most greyed-out
	// and VIP songs even though the API refuses, so one song need not fail
	// just because every imported source's API is down.
	if u, err := wyOuterURL(t.ID); err == nil {
		// NetEase's outer link serves a short clip for some songs rather than
		// refusing; the length check keeps a fragment from being played as
		// the whole song.
		if err := m.probeStream(u); err == nil && audioFits(u, time.Duration(t.Duration)*time.Millisecond) {
			return &netease.Stream{URL: u, Level: level, Type: inferType(u)}, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no enabled wy source")
	}
	return nil, lastErr
}

// wyOuterURL is NetEase's unauthenticated web-player fallback: it redirects
// to the track's audio (full length for most songs, 404 for a few).
func wyOuterURL(id int64) (string, error) {
	return "https://music.163.com/song/media/outer/url?id=" + strconv.FormatInt(id, 10) + ".mp3", nil
}

// qualitiesFor returns the qualities a platform's sources report, in the
// order their sources are tried (best first).
func (s *Source) qualitiesFor(platform string) []string {
	meta := s.Meta()
	if info, ok := meta.Sources[platform]; ok {
		return info.Qualitys
	}
	return nil
}

// wyQualityOrder returns the qualities to try, best first, for a NetEase
// level and the qualities the source offers. NetEase levels (standard/higher/
// exhigh/lossless/hires) map to source keys (128k/320k/flac/...); the best
// matching key comes first, then 128k, so a song the source only serves at
// 128k still plays without trying every quality on every source.
func wyQualityOrder(level string, quals []string) []string {
	// Source quality keys from best to worst.
	rank := []string{"hires", "flac24bit", "flac", "320k", "192k", "128k"}
	start := 3 // 320k by default
	switch {
	case level == "lossless" || level == "hires":
		start = 0
	case level == "exhigh" || level == "higher":
		start = 3
	default:
		start = 5
	}
	var out []string
	seen := map[string]bool{}
	add := func(q string) {
		if q != "" && !seen[q] {
			out = append(out, q)
			seen[q] = true
		}
	}
	for _, q := range rank[start:] {
		if hasQual(quals, q) {
			add(q)
			break
		}
	}
	// 128k is the most widely served; try it too if the best differs.
	if hasQual(quals, "128k") {
		add("128k")
	}
	// Whatever else the source lists, one more at most.
	for _, q := range quals {
		if len(out) >= 2 {
			break
		}
		add(q)
	}
	if len(out) == 0 {
		out = []string{"128k"}
	}
	return out
}

func hasQual(quals []string, q string) bool {
	for _, x := range quals {
		if x == q {
			return true
		}
	}
	return false
}

// probeStream checks that url serves audio, so a 404 page is not returned
// as a usable track. It is cheap: a ranged HEAD-free GET of a few bytes.
func (m *SourceManager) probeStream(url string) error {
	if m.probeURL != nil {
		return m.probeURL(url)
	}
	return probeStream(url)
}

// probeStream checks that url serves audio, so a 404 page is not returned
// as a usable track. It is cheap: a ranged HEAD-free GET of a few bytes.
func probeStream(url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", "bytes=0-1023")
	req.Header.Set("User-Agent", hostUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("source returned status %d", resp.StatusCode)
	}
	if strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "video/") {
		return nil
	}
	// Some hosts answer 200 with an HTML error body; reject anything that
	// does not look like media so we keep trying the next source.
	if strings.Contains(ct, "text/html") {
		return fmt.Errorf("source returned an html error page")
	}
	return nil
}

// Search queries a platform for keyword: an imported source when it declares
// a search action, otherwise the built-in Kuwo search. It returns the hits,
// or an error when every backend failed.
func (m *SourceManager) Search(platform, keyword string, page, limit int) ([]SourceTrack, error) {
	switch platform {
	case "kw":
		// Kuwo is searched natively; its public API outlives the bundled
		// sources' dead search backends.
		return KuwoSearch(keyword, page, limit)
	case "tx":
		// QQ Music, searched natively, so the 来源 dropdown returns real
		// songs even though no imported source declares a search action.
		return QQSearch(keyword, page, limit)
	}
	// LX sources read keyword/page/pagesize; older ones read limit instead.
	info := map[string]any{
		"source":   platform,
		"keyword":  keyword,
		"keywords": keyword,
		"page":     page,
		"limit":    limit,
		"pagesize": limit,
	}
	var lastErr error
	for _, src := range m.sourcesFor(platform, "search") {
		// The LX desktop contract names the action musicSearch; a source
		// that declares it expects that name, while older ones accept search.
		action := "search"
		if pi, ok := src.Meta().Sources[platform]; ok && pi.Supports("musicSearch") {
			action = "musicSearch"
		}
		v, err := src.Request(action, platform, info)
		if err != nil {
			lastErr = err
			continue
		}
		if got := parseSearchResult(v); len(got) > 0 {
			return got, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no enabled %s search source", platform)
}

// SourceTrack is a search hit from an imported source.
type SourceTrack struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Singer   string `json:"singer"`
	Album    string `json:"albumName"`
	Source   string `json:"source"`
	Interval string `json:"interval"`
	Quality  string `json:"quality"`
	// Paywalled marks a hit the platform will not stream without a paid
	// subscription, so it is shown (and treated) as unavailable rather than
	// looking playable and then failing.
	Paywalled bool `json:"paywalled,omitempty"`
}

func parseSearchResult(v any) []SourceTrack {
	arr := searchList(v)
	var out []SourceTrack
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		t := SourceTrack{
			ID:        firstString(m["id"], m["songmid"], m["hash"], m["rid"]),
			Name:      asString(m["name"]),
			Singer:    asString(m["singer"]),
			Album:     asString(m["albumName"]),
			Source:    asString(m["source"]),
			Interval:  firstString(m["interval"], m["duration"]),
			Quality:   asString(m["quality"]),
			Paywalled: asBool(m["paywalled"]),
		}
		if t.Name == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// searchList unwraps a source's search result: a plain array, or an object
// holding one. LX returns {list: [...]}, and some sources nest it under
// data or songs.
func searchList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		for _, key := range []string{"list", "songs", "data", "result", "items"} {
			if inner, ok := x[key]; ok {
				if arr := searchList(inner); len(arr) > 0 {
					return arr
				}
			}
		}
	}
	return nil
}

func firstString(vals ...any) string {
	for _, v := range vals {
		if s := asString(v); s != "" {
			return s
		}
	}
	return ""
}

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return strconv.FormatInt(int64(s), 10)
	case int64:
		return strconv.FormatInt(s, 10)
	default:
		return ""
	}
}

func asBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	case string:
		return b == "1" || b == "true"
	default:
		return false
	}
}

// inferType guesses the stream format from the URL; only mp3 and flac are
// decoded by the player.
func inferType(u string) string {
	u = strings.ToLower(u)
	switch {
	case strings.Contains(u, ".flac"):
		return "flac"
	default:
		return "mp3"
	}
}

// ResolveTrackURL resolves a track that belongs to a platform found by
// cross-platform search (kw, kg, tx, mg). Kuwo (kw) is resolved natively —
// it is also the built-in search backend — and the rest go through whatever
// imported source serves that platform.
func (m *SourceManager) ResolveTrackURL(t netease.Track, level string) (*netease.Stream, error) {
	if t.Platform == "" {
		return nil, fmt.Errorf("track has no platform")
	}
	if t.Platform == "kw" {
		u, err := KuwoURL(t.SourceID, level)
		if err != nil {
			return nil, err
		}
		return &netease.Stream{URL: u, Level: level, Type: inferType(u)}, nil
	}
	if t.Platform == "tx" {
		u, err := QQURL(t.SourceID, level)
		if err != nil {
			return nil, err
		}
		return &netease.Stream{URL: u, Level: level, Type: inferType(u)}, nil
	}
	songID := t.SourceID
	if songID == "" {
		songID = strconv.FormatInt(t.ID, 10)
	}
	info := map[string]any{
		"musicInfo": map[string]any{
			"id":        songID,
			"name":      t.Name,
			"singer":    t.ArtistNames(),
			"albumName": t.Album.Name,
			"source":    t.Platform,
			"songmid":   songID,
			"hash":      songID,
		},
		"type": level,
	}
	var lastErr error
	for _, src := range m.sourcesFor(t.Platform, "musicUrl") {
		v, err := src.Request("musicUrl", t.Platform, info)
		if err != nil {
			lastErr = err
			continue
		}
		urlStr, ok := v.(string)
		if !ok || strings.TrimSpace(urlStr) == "" {
			lastErr = fmt.Errorf("%s source returned no url", t.Platform)
			continue
		}
		return &netease.Stream{URL: strings.TrimSpace(urlStr), Level: level, Type: inferType(urlStr)}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no enabled %s source", t.Platform)
	}
	return nil, lastErr
}

// PlatformChoice is a platform a search may use, with its display name.
type PlatformChoice struct {
	ID   string
	Name string
}

// platformNames are the display names of the platforms sources serve.
var platformNames = map[string]string{
	"wy": "网易云", "kw": "酷我", "kg": "酷狗", "tx": "QQ音乐", "mg": "咪咕", "ba": "百度",
}

// builtinSearchPlatforms are platforms the app searches on its own, through
// their public APIs, when no imported source declares a search action. The
// bundled sources' search backends are dead, but Kuwo's and QQ's public APIs
// still answer, so 酷我 and QQ音乐 are always offered even with zero sources.
var builtinSearchPlatforms = []PlatformChoice{
	{ID: "kw", Name: "酷我"},
	{ID: "tx", Name: "QQ音乐"},
}

// deadSearchPlatforms are platforms a bundled source still advertises but
// whose search backend no longer answers: 汽水VIP returns HTTP 404 (checked
// 2026-10). They are left out of the 来源 dropdown so it never offers a choice
// that can only fail; their resolve action stays available.
var deadSearchPlatforms = map[string]bool{"qsvip": true}

// SearchPlatforms returns the platforms the user may search across: those an
// imported source can search, plus the built-in ones (Kuwo, QQ), each once,
// sorted by name.
func (m *SourceManager) SearchPlatforms() []PlatformChoice {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[string]bool{}
	var out []PlatformChoice
	for _, ls := range m.sources {
		if !ls.Enabled || ls.source == nil || ls.Error != "" {
			continue
		}
		for plat, info := range ls.metaFor().Sources {
			if !info.Supports("search") || seen[plat] || deadSearchPlatforms[plat] {
				continue
			}
			seen[plat] = true
			name := info.Name
			if name == "" {
				name = platformNames[plat]
			}
			if name == "" {
				name = plat
			}
			out = append(out, PlatformChoice{ID: plat, Name: name})
		}
	}
	for _, b := range builtinSearchPlatforms {
		if !seen[b.ID] {
			seen[b.ID] = true
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
