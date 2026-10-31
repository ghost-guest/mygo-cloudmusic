package app

import (
	"testing"

	"netease-music-mygo/internal/source"
)

// The 来源 choices always include the platforms the app searches natively,
// 酷我 and QQ音乐, so the dropdown can be used even with no imported source
// declaring a search action. Before this the list held only 网易云 and the
// dropdown hid itself, which looked like a missing feature.
func TestSearchSourceOptionsBuiltin(t *testing.T) {
	m := source.NewManager(t.TempDir())
	m.EnsureDefaults(nil)
	a := newTestApp()
	a.sources = m
	labels, ids := a.sourceOptions()
	if len(labels) < 3 || ids["酷我"] != "kw" || ids["QQ音乐"] != "tx" {
		t.Fatalf("labels = %v (ids %v), want 网易云, 酷我, QQ音乐", labels, ids)
	}
	// A platform whose backend is dead stays out, so no choice can only fail.
	if _, ok := ids["汽水VIP"]; ok {
		t.Fatalf("dead platform offered: %v", labels)
	}
}
