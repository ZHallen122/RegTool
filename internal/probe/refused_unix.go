//go:build !windows

package probe

import (
	"errors"
	"syscall"
)

// isConnRefused reports whether err is a connection the other end refused.
func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
