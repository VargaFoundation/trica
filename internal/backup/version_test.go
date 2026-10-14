package backup

import (
	"errors"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	const head = "--\n-- Data for Name: goose_db_version\n--\nCOPY public.goose_db_version (id, version_id, is_applied, tstamp) FROM stdin;\n"
	cases := []struct {
		name string
		rows string
		want int64
		err  error
	}{
		{"applied", "1\t0\tt\tx\n2\t1\tt\tx\n3\t2\tt\tx\n\\.\n", 2, nil},
		{"rolled back", "1\t0\tt\tx\n2\t1\tt\tx\n3\t2\tt\tx\n4\t2\tf\tx\n\\.\n", 1, nil},
		{"reapplied", "1\t0\tt\tx\n2\t1\tt\tx\n3\t1\tf\tx\n4\t1\tt\tx\n\\.\n", 1, nil},
		{"empty table", "\\.\n", 0, ErrNoVersion},
		{"malformed", "1\tnot-a-number\tt\tx\n\\.\n", 0, ErrNoVersion},
	}
	for _, c := range cases {
		got, err := parseVersion(strings.NewReader(head + c.rows))
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("%s: %d %v, want %d %v", c.name, got, err, c.want, c.err)
		}
	}
	if _, err := parseVersion(strings.NewReader("CREATE TABLE x ();\n")); !errors.Is(err, ErrNoVersion) {
		t.Errorf("no goose table: %v", err)
	}
}
