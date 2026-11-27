//go:build unix

package sig

import "syscall"

func getRcvBuf(fd uintptr) (int, error) {
	return syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF)
}
