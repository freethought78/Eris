// Package sig holds what the sender and receiver must agree on: the codec, the
// WebRTC setup, and the one-shot offer/answer exchange over HTTP.
package sig

import (
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/freethought78/eris/prototype/stream/internal/netsim"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/transport/v5"
	"github.com/pion/transport/v5/stdnet"
	"github.com/pion/webrtc/v4"
)

// Codec is the only video codec either side offers. The player can ask for a
// keyframe (PLI) but never for a re-send: waiting for one costs a round trip
// (DESIGN.md 5.6).
var Codec = webrtc.RTPCodecCapability{
	MimeType:     webrtc.MimeTypeH264,
	ClockRate:    90000,
	SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=640032",
	RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack", Parameter: "pli"}},
}

// FECCodec is the recovery-packet stream (package fec), sent alongside the
// video. Only Eris understands it.
var FECCodec = webrtc.RTPCodecCapability{MimeType: "video/x-eris-fec", ClockRate: 90000}

// Payload types for the two streams.
const (
	PayloadVideo = 102
	PayloadFEC   = 120
)

// Options say how this side connects.
type Options struct {
	// NetProfile, if set (see netsim.Parse), puts everything this side sends
	// through a simulated bad network.
	NetProfile string
	// UDPPort, if set, carries all WebRTC traffic on one UDP port, so a
	// router can forward it (the host).
	UDPPort int
	// PublicAddr, if set, is this machine's address on the internet (an IP
	// or a name), offered to the other side as well as the local ones. With
	// UDPPort forwarded on the router, a player anywhere can connect.
	PublicAddr string
	// Iface, if set, is the only network interface used. Otherwise virtual
	// ones (Docker, VPNs, bridges) are left out: they can't reach the other
	// side, and a VPN would route around the router's forwarding.
	Iface string
}

// lanIP is the IPv4 address of iface, or of the first real network card.
func lanIP(iface string) (net.IP, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || (iface != "" && i.Name != iface) || (iface == "" && virtual(i.Name)) {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
				return n.IP.To4(), nil
			}
		}
	}
	return nil, fmt.Errorf("no network card with an IPv4 address (use -iface)")
}

// virtual reports interfaces that are never the way to the other machine.
func virtual(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "wg", "tun", "tap", "lo"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// NewAPI builds a WebRTC API with just our codecs and loss reports (RTCP
// sender and receiver reports), without re-sends. When opts.NetProfile is
// set, the returned Net has the simulated network's counters.
func NewAPI(o Options) (*webrtc.API, *netsim.Net, error) {
	m := &webrtc.MediaEngine{}
	for _, c := range []webrtc.RTPCodecParameters{
		{RTPCodecCapability: Codec, PayloadType: PayloadVideo},
		{RTPCodecCapability: FECCodec, PayloadType: PayloadFEC},
	} {
		if err := m.RegisterCodec(c, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, nil, err
		}
	}
	ir := &interceptor.Registry{}
	if err := webrtc.ConfigureRTCPReports(ir); err != nil {
		return nil, nil, err
	}
	var se webrtc.SettingEngine
	var sim *netsim.Net
	var tnet transport.Net
	if o.NetProfile == "" {
		std, err := stdnet.NewNet()
		if err != nil {
			return nil, nil, err
		}
		tnet = bufNet{std}
	} else {
		p, err := netsim.Parse(o.NetProfile)
		if err != nil {
			return nil, nil, err
		}
		if sim, err = netsim.New(p); err != nil {
			return nil, nil, err
		}
		sim.OnListen = GrowBuffers
		tnet = sim
		log.Printf("simulated network for what this side sends: %s", p)
	}
	se.SetNet(tnet)
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	se.SetInterfaceFilter(func(name string) bool {
		if o.Iface != "" {
			return name == o.Iface
		}
		return !virtual(name)
	})
	if o.UDPPort != 0 {
		// Bound to the real network card's address: bound to all, the port
		// would also be offered on Docker and VPN addresses.
		ip, err := lanIP(o.Iface)
		if err != nil {
			return nil, nil, err
		}
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip, Port: o.UDPPort})
		if err != nil {
			return nil, nil, fmt.Errorf("UDP port %d: %w", o.UDPPort, err)
		}
		GrowBuffers(c)
		log.Printf("WebRTC on UDP %s:%d", ip, o.UDPPort)
		var pc net.PacketConn = c
		if sim != nil {
			pc = sim.Wrap(c)
		}
		se.SetICEUDPMux(ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: pc, Net: tnet}))
	}
	if o.PublicAddr != "" {
		ip := o.PublicAddr
		if net.ParseIP(ip) == nil {
			addrs, err := net.LookupHost(o.PublicAddr)
			if err != nil || len(addrs) == 0 {
				return nil, nil, fmt.Errorf("public address %q: %v", o.PublicAddr, err)
			}
			ip = addrs[0]
		}
		// Offered as an extra local address, so it keeps the forwarded port
		// (as a "server reflexive" one it would get a port of its own).
		if err := se.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
			External: []string{ip}, AsCandidateType: webrtc.ICECandidateTypeHost, Mode: webrtc.ICEAddressRewriteAppend,
		}); err != nil {
			return nil, nil, err
		}
		log.Printf("offering public address %s (port forwarding needed on the router)", ip)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir), webrtc.WithSettingEngine(se))
	return api, sim, nil
}

// ICEServers turns an optional STUN URL into a server list.
func ICEServers(stun string) []webrtc.ICEServer {
	if stun == "" {
		return nil
	}
	return []webrtc.ICEServer{{URLs: []string{stun}}}
}

// Offer is what the player POSTs to the host.
type Offer struct {
	SDP string `json:"sdp"`
}

// Answer is the host's reply.
type Answer struct {
	SDP    string `json:"sdp"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	FPS    int    `json:"fps"`
}

// Feedback is the player's report to the host, ten times a second, on the
// "report" data channel. It drives auto-quality (package rate).
type Feedback struct {
	Seq      uint32 `json:"seq"`
	SpanNs   int64  `json:"span"`   // time this report covers
	Bytes    int64  `json:"bytes"`  // video and recovery packets that arrived in it
	Frames   int    `json:"frames"` // frames that arrived in it
	Lost     int    `json:"lost"`   // frames given up on in it
	BehindNs int64  `json:"behind"` // median frame delay beyond the best since joining
	MaxNs    int64  `json:"max"`    // worst frame delay beyond the best
	GapNs    int64  `json:"gap"`    // longest time with no packet at all (a stall, not a slow link)
	LinkKbps int64  `json:"link"`   // link speed read off how spread out frames arrive (package pace); 0 = unknown
	EarlyN   int64  `json:"early"`  // lost video packets in the first half of their frames (pace.Tail)
	LateN    int64  `json:"late"`   // ...and in the second half
}
