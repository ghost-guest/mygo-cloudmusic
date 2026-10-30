package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"netease-music-mygo/internal/netease"
)

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

// DefaultSources are the music sources preconfigured on first launch: the
// ones that resolve NetEase (wy) streams for greyed-out or VIP songs. Order
// is the fallback priority, best first. They can be disabled or removed by
// the user; removing one from here later will not delete an already-imported
// copy unless the user removes it.
var DefaultSources = []string{
	"https://raw.githubusercontent.com/pdone/lx-music-source/main/changqing/latest.js",
	"https://raw.githubusercontent.com/pdone/lx-music-source/main/huanyin/latest.js",
	"https://raw.githubusercontent.com/pdone/lx-music-source/main/jiexiang/latest.js",
}

// disableDefaultsKey marks that the user has opted out of default sources,
// so EnsureDefaults stays a no-op even after the source list is cleared.
const disableDefaultsKey = "disable-default-sources"

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

// EnsureDefaults imports the default sources once, on first launch. It is a
// no-op afterwards: anything already in the user's source list is left
// alone, and a user who cleared them keeps them clear. Failures are logged,
// never fatal.
func (m *SourceManager) EnsureDefaults(logf func(string)) {
	m.mu.Lock()
	if m.defaultsDisabledLocked() || len(m.sources) > 0 {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	for _, url := range DefaultSources {
		slug, err := m.Add(url)
		if err != nil {
			if logf != nil {
				logf("音乐源导入失败 " + url + "：" + err.Error())
			}
			continue
		}
		if logf != nil {
			if ls := m.lookup(slug); ls != nil {
				logf("已导入音乐源 " + ls.Name)
			}
		}
	}
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
	if url == "" {
		return "", fmt.Errorf("empty source url")
	}
	slug := slugOf(url)
	js, err := loadFromURL(context.Background(), url)
	if err != nil {
		return "", fmt.Errorf("fetch source: %w", err)
	}
	path, err := saveScript(m.dir, slug, js)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ls := &LoadedSource{
		Slug:    slug,
		URL:     url,
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
// wy source in turn, so one slow or dead source does not lose the song.
func (m *SourceManager) ResolveWyURL(t netease.Track, level string) (*netease.Stream, error) {
	info := map[string]any{
		"musicInfo": map[string]any{
			"id":        t.ID,
			"name":      t.Name,
			"singer":    t.ArtistNames(),
			"albumName": t.Album.Name,
			"source":    "wy",
			"songmid":   strconv.FormatInt(t.ID, 10),
			"albumId":   t.Album.ID,
		},
		"type": level,
	}
	var lastErr error
	for _, src := range m.sourcesFor("wy", "musicUrl") {
		v, err := src.Request("musicUrl", "wy", info)
		if err != nil {
			lastErr = err
			continue
		}
		urlStr, ok := v.(string)
		if !ok || strings.TrimSpace(urlStr) == "" {
			lastErr = fmt.Errorf("wy source returned no url")
			continue
		}
		return &netease.Stream{URL: strings.TrimSpace(urlStr), Level: level, Type: inferType(urlStr)}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no enabled wy source")
	}
	return nil, lastErr
}

// Search queries an enabled platform's source for keyword.
func (m *SourceManager) Search(platform, keyword string, page, limit int) ([]SourceTrack, error) {
	info := map[string]any{
		"source":  platform,
		"keyword": keyword,
		"page":    page,
		"limit":   limit,
	}
	var lastErr error
	for _, src := range m.sourcesFor(platform, "search") {
		v, err := src.Request("search", platform, info)
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
}

func parseSearchResult(v any) []SourceTrack {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []SourceTrack
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		t := SourceTrack{
			ID:       asString(m["id"]),
			Name:     asString(m["name"]),
			Singer:   asString(m["singer"]),
			Album:    asString(m["albumName"]),
			Source:   asString(m["source"]),
			Interval: asString(m["interval"]),
			Quality:  asString(m["quality"]),
		}
		if t.Name == "" {
			continue
		}
		out = append(out, t)
	}
	return out
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

// ResolveTrackURL resolves a track that belongs to an imported source's
// platform (kw, kg, tx, mg), as a cross-platform search result does.
func (m *SourceManager) ResolveTrackURL(t netease.Track, level string) (*netease.Stream, error) {
	if t.Platform == "" {
		return nil, fmt.Errorf("track has no platform")
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

// SearchPlatforms returns the platforms an enabled source can search, each
// once, sorted by name.
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
			if !info.Supports("search") || seen[plat] {
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
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
