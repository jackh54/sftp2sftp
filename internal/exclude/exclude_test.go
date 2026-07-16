package exclude

import "testing"

func TestMatcher(t *testing.T) {
	m := New(DefaultMC...)
	cases := map[string]bool{
		"session.lock":        true,
		"world/session.lock":  true,
		"cache/chunk.dat":     true,
		"plugins/foo/cache/x": true,
		"logs/latest.log":     true,
		"server.log":          true,
		"plugins/Essentials.jar": false,
		"world/region/r.0.0.mca": false,
	}

	for p, want := range cases {
		if got := m.Match(p); got != want {
			t.Fatalf("Match(%q) = %v, want %v", p, got, want)
		}
	}
}
