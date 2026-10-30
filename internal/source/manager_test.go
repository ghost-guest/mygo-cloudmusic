package source

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"netease-music-mygo/internal/netease"
)

// ResolveWyURL uses an imported wy source for a NetEase track, and Search
// goes through the host's request to a local server. No external network.
func TestManagerResolveAndSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"list":[{"name":"SongA","singer":"ArtistB","album":"AlbumC","id":"x1"}]}`))
	}))
	defer srv.Close()

	js := fmt.Sprintf(`
const { EVENT_NAMES, request, on, send } = globalThis.lx
on(EVENT_NAMES.request, ({ source, action, info }) => {
  if (action === 'musicUrl') return 'https://cdn.example.com/' + info.musicInfo.id + '.mp3'
  if (action === 'search') return new Promise((resolve, reject) => {
    request(%q, { method: 'GET' }, (err, resp) => {
      if (err) return reject(new Error(err))
      resolve(resp.body.list.map(x => ({ id: x.id, name: x.name, singer: x.singer, albumName: x.album, source: 'mg' })))
    })
  })
  return Promise.reject(new Error('no ' + action))
})
send(EVENT_NAMES.inited, { sources: {
  wy: { name: '网易云', type: 'music', actions: ['musicUrl'], qualitys: ['128k'] },
  mg: { name: '咪咕', type: 'music', actions: ['search'], qualitys: ['128k'] }
} })
`, srv.URL)

	dir := t.TempDir()
	m := NewManager(dir)
	path, err := saveScript(dir, "test", []byte(js))
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	ls := &LoadedSource{Slug: "test", File: path, Enabled: true}
	m.sources["test"] = ls
	m.startLocked(ls)
	m.probeURL = func(url string) error { return nil }
	m.mu.Unlock()
	if ls.Error != "" {
		t.Fatalf("start: %s", ls.Error)
	}
	defer ls.source.Close()

	st, err := m.ResolveWyURL(netease.Track{ID: 42, Name: "S", Album: netease.Album{Name: "A"}}, "128k")
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://cdn.example.com/42.mp3" || st.Type != "mp3" {
		t.Fatalf("stream = %+v", st)
	}

	got, err := m.Search("mg", "song", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "SongA" || got[0].Singer != "ArtistB" || got[0].Source != "mg" {
		t.Fatalf("search = %+v", got)
	}
}

// EnsureDefaults writes the bundled sources from the embed, so a first
// launch needs no network, and every one of them loads in the sandbox.
func TestManagerEnsureDefaultsBundled(t *testing.T) {
	m := NewManager(t.TempDir())
	m.EnsureDefaults(func(s string) { t.Log(s) })
	list := m.List()
	if len(list) != len(DefaultSources) {
		t.Fatalf("want %d bundled sources, got %d", len(DefaultSources), len(list))
	}
	for _, ls := range list {
		if !ls.Enabled {
			t.Errorf("%s not enabled", ls.Slug)
		}
		if ls.Error != "" {
			t.Errorf("%s failed to load: %s", ls.Slug, ls.Error)
		}
	}
	if !m.HasWy() {
		t.Fatal("want a wy source after defaults")
	}
	// A second call is a no-op: no duplicates.
	m.EnsureDefaults(nil)
	if got := len(m.List()); got != len(DefaultSources) {
		t.Fatalf("second call added sources: %d", got)
	}
}

// A disabled source is not picked for a fallback.
func TestManagerDisabledNotPicked(t *testing.T) {
	js := `
const { EVENT_NAMES, on, send } = globalThis.lx
on(EVENT_NAMES.request, () => 'https://cdn.example.com/a.mp3')
send(EVENT_NAMES.inited, { sources: { wy: { name: 'wy', type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } } })
`
	dir := t.TempDir()
	m := NewManager(dir)
	path, _ := saveScript(dir, "test", []byte(js))
	m.mu.Lock()
	ls := &LoadedSource{Slug: "test", File: path, Enabled: false}
	m.sources["test"] = ls
	m.startLocked(ls)
	// The source is disabled, so only the outer-URL last resort could answer;
	// make its probe fail to prove no source was used.
	m.probeURL = func(url string) error { return errors.New("no") }
	m.mu.Unlock()
	defer ls.source.Close()
	if _, err := m.ResolveWyURL(netease.Track{ID: 1}, "128k"); err == nil {
		t.Fatal("want an error with the source disabled")
	}
}

// ResolveWyURL tries wy sources in import order, so a source that fails is
// skipped rather than losing the song, and the first one to answer wins.
func TestManagerWyFallbackOrder(t *testing.T) {
	js := func(ok bool) string {
		return fmt.Sprintf(`
const { EVENT_NAMES, on, send } = globalThis.lx
on(EVENT_NAMES.request, ({ action, info }) => {
  if (action !== 'musicUrl') return Promise.reject(new Error('no ' + action))
  if (!%t) return Promise.reject(new Error('down'))
  return 'https://cdn.example.com/' + info.musicInfo.id + '.mp3'
})
send(EVENT_NAMES.inited, { sources: { wy: { name: 'wy', type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } } })
`, ok)
	}
	dir := t.TempDir()
	m := NewManager(dir)
	// The first source imported is down; the second answers.
	first, _ := saveScript(dir, "first", []byte(js(false)))
	second, _ := saveScript(dir, "second", []byte(js(true)))
	m.mu.Lock()
	ls1 := &LoadedSource{Slug: "first", File: first, Enabled: true}
	ls2 := &LoadedSource{Slug: "second", File: second, Enabled: true}
	m.sources["first"], m.sources["second"] = ls1, ls2
	m.order["first"], m.order["second"] = 0, 1
	m.nextOrder = 2
	m.startLocked(ls1)
	m.startLocked(ls2)
	m.probeURL = func(url string) error { return nil }
	m.mu.Unlock()
	defer ls1.source.Close()
	defer ls2.source.Close()

	st, err := m.ResolveWyURL(netease.Track{ID: 7}, "128k")
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://cdn.example.com/7.mp3" {
		t.Fatalf("want the second source's url, got %s", st.URL)
	}

	// With both up, the first imported source wins.
	ls1.source.Close()
	first2, _ := saveScript(dir, "first2", []byte(js(true)))
	m.mu.Lock()
	ls1b := &LoadedSource{Slug: "first2", File: first2, Enabled: true}
	m.sources["first2"] = ls1b
	m.order["first2"] = 0
	m.nextOrder = 3
	m.startLocked(ls1b)
	m.mu.Unlock()
	defer ls1b.source.Close()
	st, err = m.ResolveWyURL(netease.Track{ID: 8}, "128k")
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://cdn.example.com/8.mp3" {
		t.Fatalf("want the first source's url, got %s", st.URL)
	}
}

// EnsureDefaults never re-imports once the user has cleared the list.
func TestManagerEnsureDefaultsDisabled(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.DisableDefaults()
	called := false
	m.EnsureDefaults(func(string) { called = true })
	if called {
		t.Fatal("defaults imported after being disabled")
	}
	if len(m.List()) != 0 {
		t.Fatal("defaults imported after being disabled")
	}
}
