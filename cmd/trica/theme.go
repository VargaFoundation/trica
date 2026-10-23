package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/VargaFoundation/trica/internal/theme"
)

// runTheme is `trica theme check <zip|dir>`: it runs the checks the server
// runs when it loads a theme, and prints every reason a theme is refused, so a
// theme is written and fixed on a workstation, not by restarting a pod.
func runTheme(args []string, stdout, stderr io.Writer) error {
	if len(args) != 2 || args[0] != "check" {
		fmt.Fprintln(stderr, "usage: trica theme check <theme.zip | directory>")
		return errors.New("usage: trica theme check <theme.zip | directory>")
	}
	t, err := theme.ReadPath(args[1])
	if err != nil {
		var r *theme.RefusedError
		if errors.As(err, &r) {
			for _, v := range r.Violations {
				fmt.Fprintln(stderr, "refused:", v)
			}
			return fmt.Errorf("theme refused: %d problem(s)", len(r.Violations))
		}
		return err
	}
	fmt.Fprintf(stdout, "ok: %s %s\nsha256: %s\nstylesheet: %d bytes\nfiles served: %d\n", t.Name, t.Version, t.SHA, len(t.CSS()), len(t.Files()))
	return nil
}
