//go:build !unix

package update

import "errors"

// Exec is not supported off Unix: the agent is a Linux program.
func Exec(string, []string, []string) error {
	return errors.New("exec is not supported on this platform")
}
