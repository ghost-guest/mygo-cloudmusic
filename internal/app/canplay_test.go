package app

import (
	"testing"

	"netease-music-mygo/internal/netease"
	"netease-music-mygo/internal/source"
)

// A greyed-out VIP song plays when an enabled wy source can resolve it; a
// source-platform track plays through its own source unless the platform
// gates it (a paid QQ song); only without either does it stay grey.
func TestCanPlay(t *testing.T) {
	a := newTestApp()
	vip := netease.Track{ID: 1, Fee: 1}
	normal := netease.Track{ID: 2, Fee: 0}
	platform := netease.Track{ID: 0, Fee: 0, Platform: "kw", SourceID: "x1"}
	paid := netease.Track{ID: 0, Fee: 1, Platform: "tx", SourceID: "x2"}

	if !a.canPlay(normal) {
		t.Fatal("a normal track is playable")
	}
	if a.canPlay(vip) {
		t.Fatal("a VIP track is not playable without a source")
	}
	if !a.canPlay(platform) {
		t.Fatal("a source-platform track is playable")
	}
	if a.canPlay(paid) {
		t.Fatal("a paid source-platform track is not playable")
	}

	// The bundled defaults load offline and serve wy.
	m := source.NewManager(t.TempDir())
	m.EnsureDefaults(nil)
	a.sources = m
	if !a.canPlay(vip) {
		t.Fatal("a VIP track is playable with a wy source")
	}
	if !a.canPlay(normal) {
		t.Fatal("a normal track stays playable")
	}
	if a.firstPlayable([]netease.Track{vip}) != 0 {
		t.Fatal("a VIP track is picked first with a source")
	}
}
