package sig

import (
	"log"
	"net"
	"sync"
	"syscall"

	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
)

// SocketBuffer is the receive and send buffer asked for on every WebRTC UDP
// socket. When a Wi-Fi stall clears, everything held up arrives at once (at
// 12 Mbps, 100 ms is about 150 packets); the system default (208 KB on
// Linux, less on Windows) overflows and drops them. Linux grants at most
// net.core.rmem_max.
const SocketBuffer = 4 << 20

// bufNet is the standard network with bigger socket buffers.
type bufNet struct{ *stdnet.Net }

var bufOnce sync.Once

// GrowBuffers asks for SocketBuffer on a UDP socket and, once per process,
// logs what the system granted.
func GrowBuffers(c transport.UDPConn) {
	_ = c.SetReadBuffer(SocketBuffer)
	_ = c.SetWriteBuffer(SocketBuffer)
	bufOnce.Do(func() {
		sc, ok := c.(interface {
			SyscallConn() (syscall.RawConn, error)
		})
		if !ok {
			return
		}
		raw, err := sc.SyscallConn()
		if err != nil {
			return
		}
		_ = raw.Control(func(fd uintptr) {
			if n, err := getRcvBuf(fd); err == nil {
				// Linux reports double the usable size.
				log.Printf("UDP receive buffer: %d KB (asked for %d KB)", n/1024, SocketBuffer/1024)
			}
		})
	})
}

func (n bufNet) ListenUDP(network string, laddr *net.UDPAddr) (transport.UDPConn, error) {
	c, err := n.Net.ListenUDP(network, laddr)
	if err == nil {
		GrowBuffers(c)
	}
	return c, err
}
