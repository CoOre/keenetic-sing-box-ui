package update

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.11.0", "1.12.0", -1},
		{"1.12.0", "1.11.9", 1},
		{"1.12.0", "1.12.0", 0},
		{"v0.1.2", "0.1.3", -1},
		{"0.1.10", "0.1.9", 1},
		{"1.12", "1.12.0", 0},
		// git-describe dev build ahead of the tag is NOT older than the tag
		{"0.1.3-5-gabc123", "0.1.3", 1},
		{"0.1.3-5-gabc123-dirty", "0.1.3", 1},
		{"0.1.3-dirty", "0.1.3", 0},
		// but a dev build of an older tag still updates
		{"0.1.2-5-gabc123", "0.1.3", -1},
		// prereleases are behind their release
		{"1.12.0-beta.1", "1.12.0", -1},
		// unparsable versions are not comparable
		{"dev", "0.1.3", 0},
		{"", "0.1.3", 0},
		{"unknown", "1.2.3", 0},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
