//go:build windows

package probe

import (
	"errors"
	"syscall"
)

// wsaeconnrefused is WSAECONNREFUSED, the Windows sockets code for a refused
// connection. It has to be spelled out here: the syscall package does not
// export it, and the syscall.ECONNREFUSED it does export is a different number
// on Windows, so a dial that was refused never matches it.
const wsaeconnrefused = syscall.Errno(10061)

// isConnRefused reports whether err is a connection the other end refused.
func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaeconnrefused)
}
