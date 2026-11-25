package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/VargaFoundation/trica/internal/collect/catalog"
)

// runCatalog is `trica catalog check <file|dir>`: the checks a catalog target
// runs before it declares anything (ADR 0071), so that a catalog is checked
// in the merge request that changes it, not by a failed run in production.
// It reads no database and contacts no server.
func runCatalog(args []string, stdout, stderr io.Writer) error {
	const use = "usage: trica catalog check <catalog.yaml | directory>"
	if len(args) != 2 || args[0] != "check" {
		fmt.Fprintln(stderr, use)
		return errors.New(use)
	}
	doc, files, err := catalog.ReadPath(args[1])
	if err != nil {
		return err
	}
	if err := doc.Validate(); err != nil {
		problems := strings.Split(err.Error(), "\n")
		for _, p := range problems {
			fmt.Fprintln(stderr, "refused:", p)
		}
		return fmt.Errorf("catalog refused: %d problem(s)", len(problems))
	}
	if len(doc.Applications) == 0 {
		fmt.Fprintln(stderr, "refused: the catalog declares no application (a target accepts it only with options.allow_empty, and it then withdraws every application declared before)")
		return errors.New("catalog refused: empty")
	}
	instances := 0
	for _, a := range doc.Applications {
		instances += len(a.Instances)
	}
	fmt.Fprintf(stdout, "ok: %s\napplications: %d\nteams: %d\npeople: %d\nenvironments: %d\ninstances: %d\n",
		strings.Join(files, ", "), len(doc.Applications), len(doc.Teams), len(doc.People), len(doc.Environments), instances)
	return nil
}
