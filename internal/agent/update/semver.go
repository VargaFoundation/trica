package update

import (
	"fmt"
	"regexp"
	"strconv"
)

var semverRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$`)

// parseVersion reads MAJOR.MINOR.PATCH, nothing else: no "v" prefix, no
// pre-release. The strictness is also what keeps a version safe to use as a
// directory name.
func parseVersion(v string) ([3]int, error) {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, fmt.Errorf("%q is not MAJOR.MINOR.PATCH", v)
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, nil
}

// newer reports whether a is strictly greater than b.
func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
