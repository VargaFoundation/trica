// Package docscheck holds tests on the repository's documentation, run by make ci.
package docscheck

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

var statusRow = regexp.MustCompile(`(?m)^\| *(\d+) *\|`)

// TestStatusRowsUniqueAndOrdered guards docs/plan/STATUS.md: rows are cited by number from
// ADRs and reference pages, so a number used twice (as parallel merges once left) makes
// "STATUS row N" ambiguous.
func TestStatusRowsUniqueAndOrdered(t *testing.T) {
	b, err := os.ReadFile("../../docs/plan/STATUS.md")
	if err != nil {
		t.Fatal(err)
	}
	checkStatusRows(t, string(b))
}

func checkStatusRows(t testing.TB, doc string) {
	t.Helper()
	m := statusRow.FindAllStringSubmatch(doc, -1)
	if len(m) == 0 {
		t.Fatal("no numbered row found")
	}
	prev := 0
	for _, r := range m {
		n, _ := strconv.Atoi(r[1])
		if n <= prev {
			t.Errorf("row %d follows row %d: numbers must be unique and increasing", n, prev)
		}
		prev = n
	}
}

// TestCheckStatusRowsRejects proves the check fails on the defects it exists for.
func TestCheckStatusRowsRejects(t *testing.T) {
	for name, doc := range map[string]string{
		"duplicate": "| 1 | a |\n| 2 | b |\n| 2 | c |\n",
		"order":     "| 1 | a |\n| 3 | b |\n| 2 | c |\n",
		"empty":     "no table\n",
	} {
		rec := &recorder{TB: t}
		func() {
			defer func() { _ = recover() }()
			checkStatusRows(rec, doc)
		}()
		if !rec.failed {
			t.Errorf("%s: accepted", name)
		}
	}
}

// recorder notes a failure instead of failing the enclosing test; Fatal stops the check
// by panicking, which the caller recovers.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Helper()               {}
func (r *recorder) Errorf(string, ...any) { r.failed = true }
func (r *recorder) Fatal(...any)          { r.failed = true; panic("fatal") }
