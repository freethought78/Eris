// Package sig holds what the sender and receiver must agree on: the codec, the
// WebRTC setup, and the one-shot offer/answer exchange over HTTP.
package sig

import (
	"log"

	"github.com/freethought78/eris/prototype/stream/internal/netsim"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
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

// NewAPI builds a WebRTC API with just our codecs and loss reports (RTCP
// sender and receiver reports), without re-sends. If netProfile is set
// (see netsim.Parse), everything this side sends goes through a simulated bad
// network; the returned Net has its counters.
func NewAPI(netProfile string) (*webrtc.API, *netsim.Net, error) {
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
	opts := []func(*webrtc.API){webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir)}
	var sim *netsim.Net
	var se webrtc.SettingEngine
	if netProfile == "" {
		std, err := stdnet.NewNet()
		if err != nil {
			return nil, nil, err
		}
		se.SetNet(bufNet{std})
		opts = append(opts, webrtc.WithSettingEngine(se))
	} else {
		p, err := netsim.Parse(netProfile)
		if err != nil {
			return nil, nil, err
		}
		if sim, err = netsim.New(p); err != nil {
			return nil, nil, err
		}
		sim.OnListen = GrowBuffers
		se.SetNet(sim)
		se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
		opts = append(opts, webrtc.WithSettingEngine(se))
		log.Printf("simulated network for what this side sends: %s", p)
	}
	return webrtc.NewAPI(opts...), sim, nil
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
