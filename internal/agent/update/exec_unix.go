//go:build unix

package update

import "syscall"

// Exec replaces the current process with the binary at path, with the same
// arguments, and the environment plus BootedEnv so that the new process does
// not boot again.
func Exec(path string, args, env []string) error {
	return syscall.Exec(path, args, append(env, BootedEnv+"=1")) //nolint:gosec // path is built by Store.BinaryPath from a validated version
}
