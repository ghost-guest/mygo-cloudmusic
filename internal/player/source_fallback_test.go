package player

import (
	"errors"
	"testing"

	"netease-music-mygo/internal/netease"
)

// A fallback supplies the stream where NetEase has none.
func TestStreamForFallback(t *testing.T) {
	p := New(func(func()) {})
	p.songURL = func(id int64, level string) (*netease.Stream, error) {
		return nil, netease.ErrUnavailable
	}
	var used string
	p.StreamFallback = func(tk netease.Track, level string) (*netease.Stream, error) {
		used = level
		return &netease.Stream{URL: "https://cdn.example.com/fb.mp3", Level: level, Type: "mp3"}, nil
	}
	info, err := p.streamFor(netease.Track{ID: 7}, "exhigh")
	if err != nil {
		t.Fatal(err)
	}
	if used != "exhigh" {
		t.Fatalf("fallback level = %q", used)
	}
	if info.URL != "https://cdn.example.com/fb.mp3" {
		t.Fatalf("url = %q", info.URL)
	}
}

// A non-availability error is not sent to the fallback.
func TestStreamForHardError(t *testing.T) {
	p := New(func(func()) {})
	p.songURL = func(id int64, level string) (*netease.Stream, error) {
		return nil, errors.New("network down")
	}
	called := false
	p.StreamFallback = func(tk netease.Track, level string) (*netease.Stream, error) {
		called = true
		return nil, nil
	}
	_, err := p.streamFor(netease.Track{ID: 7}, "exhigh")
	if err == nil || called {
		t.Fatalf("want hard error, fallback called=%v err=%v", called, err)
	}
}

// With no fallback, an unavailable track stays unavailable.
func TestStreamForNoFallback(t *testing.T) {
	p := New(func(func()) {})
	p.songURL = func(id int64, level string) (*netease.Stream, error) {
		return nil, netease.ErrUnavailable
	}
	if _, err := p.streamFor(netease.Track{ID: 7}, "exhigh"); err != netease.ErrUnavailable {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}
