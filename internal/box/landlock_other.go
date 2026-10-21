//go:build !linux

package box

// landlockABI is -1 on an operating system that has no Landlock.
func landlockABI() int { return -1 }
