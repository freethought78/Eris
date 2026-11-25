// Package sig holds what the sender and receiver must agree on: the codec, the
// WebRTC setup, and the one-shot offer/answer exchange over HTTP.
package sig

import (
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

// Codec is the only video codec either side offers.
var Codec = webrtc.RTPCodecCapability{
	MimeType:    webrtc.MimeTypeH264,
	ClockRate:   90000,
	SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=640032",
	RTCPFeedback: []webrtc.RTCPFeedback{
		{Type: "nack"}, {Type: "nack", Parameter: "pli"},
	},
}

// NewAPI builds a WebRTC API with just our codec and the default interceptors
// (packet-loss reports, re-send requests, statistics).
func NewAPI() (*webrtc.API, error) {
	m := &webrtc.MediaEngine{}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: Codec, PayloadType: 102},
		webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
		return nil, err
	}
	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir)), nil
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
