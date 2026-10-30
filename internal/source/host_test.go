package source

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A source that answers musicUrl synchronously is served as its URL.
func TestSourceMusicURLSync(t *testing.T) {
	js := `
const { EVENT_NAMES, on, send } = globalThis.lx
on(EVENT_NAMES.request, ({ source, action, info }) => {
  if (action !== 'musicUrl') return Promise.reject(new Error('no ' + action))
  if (source !== 'wy') return Promise.reject(new Error('no ' + source))
  if (info.type !== '320k') return Promise.reject(new Error('bad type'))
  return 'https://cdn.example.com/song.mp3'
})
send(EVENT_NAMES.inited, { sources: { wy: { name: '网易云', type: 'music', actions: ['musicUrl'], qualitys: ['128k', '320k'] } } })
`
	s, err := runSource(js, SourceMeta{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	wy, ok := s.Meta().Sources["wy"]
	if !ok || !wy.Supports("musicUrl") {
		t.Fatalf("wy not reported: %+v", s.Meta().Sources)
	}
	got, err := s.Request("musicUrl", "wy", map[string]any{"musicInfo": map[string]any{"id": 1}, "type": "320k"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example.com/song.mp3" {
		t.Fatalf("got %v", got)
	}
}

// A source that waits on setTimeout before answering is still served.
func TestSourceMusicURLAwaitsTimer(t *testing.T) {
	js := `
const { EVENT_NAMES, on, send } = globalThis.lx
function sleep(ms) { return new Promise(r => setTimeout(r, ms)) }
on(EVENT_NAMES.request, async ({ action }) => {
  await sleep(15)
  return 'https://cdn.example.com/slow.mp3'
})
send(EVENT_NAMES.inited, { sources: { wy: { name: 'wy', type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } } })
`
	s, err := runSource(js, SourceMeta{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Request("musicUrl", "wy", map[string]any{"type": "128k"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example.com/slow.mp3" {
		t.Fatalf("got %v", got)
	}
}

// A source that calls the host's callback-style request gets the parsed
// JSON body back.
func TestSourceUsesHostRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":200,"url":"https://cdn.example.com/api.mp3"}`))
	}))
	defer srv.Close()

	js := `
const { EVENT_NAMES, request, on, send } = globalThis.lx
on(EVENT_NAMES.request, ({ action, info }) => new Promise((resolve, reject) => {
  request(info.api, { method: 'GET', headers: { 'X-Test': '1' } }, (err, resp) => {
    if (err) return reject(new Error(String(err)))
    if (resp.body.code !== 200) return reject(new Error('bad code'))
    resolve(resp.body.url)
  })
}))
send(EVENT_NAMES.inited, { sources: { wy: { name: 'wy', type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } } })
`
	s, err := runSource(js, SourceMeta{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Request("musicUrl", "wy", map[string]any{"api": srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://cdn.example.com/api.mp3" {
		t.Fatalf("got %v", got)
	}
}

// A source that rejects surfaces the error message.
func TestSourceRejects(t *testing.T) {
	js := `
const { EVENT_NAMES, on, send } = globalThis.lx
on(EVENT_NAMES.request, () => Promise.reject(new Error('blocked')))
send(EVENT_NAMES.inited, { sources: { wy: { name: 'wy', type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } } })
`
	s, err := runSource(js, SourceMeta{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.Request("musicUrl", "wy", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("want blocked error, got %v", err)
	}
}

// A script with no request handler fails to load.
func TestSourceWithoutHandlerFails(t *testing.T) {
	if _, err := runSource(`1`, SourceMeta{}); err == nil {
		t.Fatal("want an error for a source with no handler")
	}
}

// utils.buffer.from(...) then bufToString(..., 'base64') round-trips, as
// several sources build request tokens.
func TestSourceBufferUtil(t *testing.T) {
	js := `
const { EVENT_NAMES, on, send, utils } = globalThis.lx
on(EVENT_NAMES.request, ({ action }) => {
  const buf = utils.buffer.from(JSON.stringify({a: 1}), 'utf-8');
  return utils.buffer.bufToString(buf, 'base64');
})
send(EVENT_NAMES.inited, { sources: { wy: { name: 'wy', type: 'music', actions: ['musicUrl'], qualitys: ['128k'] } } })
`
	s, err := runSource(js, SourceMeta{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Request("musicUrl", "wy", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := got.(string)
	dec, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("not base64: %v (%q)", err, got)
	}
	if string(dec) != `{"a":1}` {
		t.Fatalf("decoded %q", dec)
	}
}
