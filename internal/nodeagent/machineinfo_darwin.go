//go:build darwin

package nodeagent

import "golang.org/x/sys/unix"

// totalMemoryBytes reads the machine's physical memory via the "hw.memsize"
// sysctl.
//
// This deliberately goes through golang.org/x/sys/unix.SysctlUint64 rather
// than the standard library's syscall.Sysctl: that stdlib function is built
// for *string*-valued sysctls and trims what it assumes is a trailing NUL
// byte — for an 8-byte integer sysctl like this one, whenever the
// most-significant (last, on this little-endian architecture) byte happens
// to be zero, it would silently truncate the value by a byte. x/sys/unix is
// the correct, Go-team-maintained way to read an integer sysctl.
func totalMemoryBytes() (int64, error) {
	bytes, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0, err
	}
	return int64(bytes), nil
}
