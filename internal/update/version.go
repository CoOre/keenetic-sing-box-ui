// Package update checks GitHub releases for newer versions of sing-box and
// of this UI itself, and optionally installs them automatically.
package update

import (
	"regexp"
	"strconv"
	"strings"
)

// gitAheadRe matches the suffix `git describe` appends to a tag when the
// build is ahead of it: "<N>-g<hash>[-dirty]". Such a build is NEWER than the
// tag it is based on, not older.
var gitAheadRe = regexp.MustCompile(`^\d+-g[0-9a-f]+(-dirty)?$`)

// CompareVersions orders two version strings ("v" prefix optional):
// -1 — a is older than b, +1 — a is newer, 0 — equal or not comparable.
// Handles numeric dotted bases, git-describe suffixes (treated as ahead of
// the base tag) and prerelease suffixes (treated as behind the base tag).
func CompareVersions(a, b string) int {
	aBase, aRest, aOK := splitVersion(a)
	bBase, bRest, bOK := splitVersion(b)
	if !aOK || !bOK {
		return 0
	}
	for i := 0; i < len(aBase) || i < len(bBase); i++ {
		av, bv := 0, 0
		if i < len(aBase) {
			av = aBase[i]
		}
		if i < len(bBase) {
			bv = bBase[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return compareRest(aRest, bRest)
}

// compareRest orders suffixes of versions with equal numeric bases:
// git-describe-ahead > release (empty) > prerelease.
func compareRest(a, b string) int {
	rank := func(rest string) int {
		switch {
		case rest == "" || rest == "dirty":
			return 1 // release build of the tag itself
		case gitAheadRe.MatchString(rest):
			return 2 // dev build ahead of the tag
		default:
			return 0 // prerelease (alpha/beta/rc)
		}
	}
	ar, br := rank(a), rank(b)
	if ar < br {
		return -1
	}
	if ar > br {
		return 1
	}
	return 0
}

// splitVersion parses "v1.2.3-rest" into numeric base segments and the rest.
func splitVersion(v string) (base []int, rest string, ok bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if v == "" {
		return nil, "", false
	}
	numPart, rest, _ := strings.Cut(v, "-")
	for _, seg := range strings.Split(numPart, ".") {
		n, err := strconv.Atoi(seg)
		if err != nil {
			return nil, "", false
		}
		base = append(base, n)
	}
	return base, rest, true
}
