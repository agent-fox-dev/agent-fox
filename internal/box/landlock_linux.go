//go:build linux

package box

import "syscall"

// landlockABI is the Landlock ABI of the kernel: the answer of the version query of
// landlock_create_ruleset (syscall 444, no attributes, flag
// LANDLOCK_CREATE_RULESET_VERSION), which creates nothing and confines nothing. It
// is -1 when the kernel has no Landlock, or the call is not allowed.
func landlockABI() int {
	abi, _, errno := syscall.Syscall(444, 0, 0, 1)
	if errno != 0 {
		return -1
	}
	return int(abi)
}
