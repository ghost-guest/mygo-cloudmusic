package source

import "testing"

// A search hit that is a preview, live take or remix must never be chosen for
// an unlock: Kuwo served those as an 85KB clip and the song played as a
// fragment of something else. The full studio song wins even when a clip
// ranks first.
func TestRankHitsSkipsFragments(t *testing.T) {
	hits := []SourceTrack{
		{ID: "MUSIC_1", Name: "七里香 (42秒片段)", Singer: "周杰伦"},
		{ID: "MUSIC_2", Name: "七里香 (Live)", Singer: "周杰伦"},
		{ID: "MUSIC_3", Name: "七里香 (DJ版)", Singer: "周杰伦"},
		{ID: "MUSIC_4", Name: "七里香", Singer: "周杰伦"},
	}
	got := rankHits("七里香", "周杰伦", hits)
	if len(got) != 1 || got[0].ID != "MUSIC_4" {
		t.Fatalf("ranked = %+v, want only the studio song", got)
	}
}

// An exact name with the right singer beats an exact name by someone else,
// and a cover by another singer is still usable when nothing else exists
// under the song's own name.
func TestRankHitsPrefersSinger(t *testing.T) {
	hits := []SourceTrack{
		{ID: "a", Name: "晴天", Singer: "群星"},
		{ID: "b", Name: "晴天", Singer: "周杰伦"},
	}
	if got := rankHits("晴天", "周杰伦", hits); got[0].ID != "b" {
		t.Fatalf("first = %q, want the song's own singer", got[0].ID)
	}
	if got := rankHits("晴天", "周杰伦", hits[0:1]); len(got) != 1 {
		t.Fatalf("a name-only exact match should still be offered, got %+v", got)
	}
	// A different song whose name merely contains the query is not it.
	if got := rankHits("晴天", "周杰伦", []SourceTrack{{ID: "c", Name: "晴天娃娃", Singer: "周杰伦"}}); len(got) != 0 {
		t.Fatalf("a longer title matched: %+v", got)
	}
}

// The mobile endpoint's br codes, so a quality request maps onto Kuwo's own
// names instead of falling back to a default.
func TestKuwoBitrate(t *testing.T) {
	for level, want := range map[string]string{
		"128k": "128kmp3", "320k": "320kmp3", "flac": "2000kflac",
		"hires": "4000kflac", "": "320kmp3", "exhigh": "320kmp3",
	} {
		if got := kuwoBitrate(level); got != want {
			t.Errorf("kuwoBitrate(%q) = %q, want %q", level, got, want)
		}
	}
}
