// Package opus encodes and decodes the game's sound for Play Together (48 kHz
// stereo, 10 ms packets) with FFmpeg's libraries: libopus to encode (the
// host, Linux), FFmpeg's own Opus decoder to decode (the player; it's in
// every FFmpeg build, Windows included).
package opus

/*
#cgo pkg-config: libavcodec libavutil
#include <stdlib.h>
#include <string.h>
#include <libavcodec/avcodec.h>
#include <libavutil/channel_layout.h>
#include <libavutil/opt.h>

static int eris_opus_enc(AVCodecContext **out, int kbps) {
	const AVCodec *codec = avcodec_find_encoder_by_name("libopus");
	if (!codec) return AVERROR_ENCODER_NOT_FOUND;
	AVCodecContext *c = avcodec_alloc_context3(codec);
	if (!c) return AVERROR(ENOMEM);
	c->sample_rate = 48000;
	c->sample_fmt = AV_SAMPLE_FMT_S16;
	c->bit_rate = kbps * 1000;
	av_channel_layout_default(&c->ch_layout, 2);
	av_opt_set(c->priv_data, "application", "lowdelay", 0);
	av_opt_set_double(c->priv_data, "frame_duration", 10, 0);
	av_opt_set_int(c->priv_data, "fec", 1, 0);
	av_opt_set_int(c->priv_data, "packet_loss", 5, 0);
	int err = avcodec_open2(c, codec, NULL);
	if (err < 0) { avcodec_free_context(&c); return err; }
	*out = c;
	return 0;
}

// pcm: 480 interleaved stereo s16 samples. Returns the packet size, 0 when
// none is ready, <0 on error.
static int eris_opus_encode(AVCodecContext *c, AVFrame *f, AVPacket *p, const int16_t *pcm, uint8_t *dst, int cap) {
	f->nb_samples = 480;
	f->format = AV_SAMPLE_FMT_S16;
	f->sample_rate = 48000;
	av_channel_layout_default(&f->ch_layout, 2);
	int err = av_frame_get_buffer(f, 0);
	if (err < 0) return err;
	memcpy(f->data[0], pcm, 480 * 2 * 2);
	err = avcodec_send_frame(c, f);
	av_frame_unref(f);
	if (err < 0) return err;
	err = avcodec_receive_packet(c, p);
	if (err == AVERROR(EAGAIN)) return 0;
	if (err < 0) return err;
	int n = p->size;
	if (n > cap) n = cap;
	memcpy(dst, p->data, n);
	av_packet_unref(p);
	return n;
}

static int eris_opus_dec(AVCodecContext **out) {
	const AVCodec *codec = avcodec_find_decoder(AV_CODEC_ID_OPUS);
	if (!codec) return AVERROR_DECODER_NOT_FOUND;
	AVCodecContext *c = avcodec_alloc_context3(codec);
	if (!c) return AVERROR(ENOMEM);
	c->sample_rate = 48000;
	av_channel_layout_default(&c->ch_layout, 2);
	int err = avcodec_open2(c, codec, NULL);
	if (err < 0) { avcodec_free_context(&c); return err; }
	*out = c;
	return 0;
}

// Decodes one packet into dst as interleaved stereo float. Returns the
// number of samples per channel, <0 on error.
static int eris_opus_decode(AVCodecContext *c, AVFrame *f, AVPacket *p, const uint8_t *data, int size, float *dst, int cap) {
	int err = av_new_packet(p, size);
	if (err < 0) return err;
	memcpy(p->data, data, size);
	err = avcodec_send_packet(c, p);
	av_packet_unref(p);
	if (err < 0) return err;
	int total = 0;
	for (;;) {
		err = avcodec_receive_frame(c, f);
		if (err == AVERROR(EAGAIN) || err == AVERROR_EOF) break;
		if (err < 0) return err;
		int ch = f->ch_layout.nb_channels;
		int n = f->nb_samples;
		if (total + n > cap) n = cap - total;
		for (int i = 0; i < n; i++) {
			float l, r;
			if (f->format == AV_SAMPLE_FMT_FLTP) {
				l = ((float *)f->data[0])[i];
				r = ch > 1 ? ((float *)f->data[1])[i] : l;
			} else if (f->format == AV_SAMPLE_FMT_FLT) {
				l = ((float *)f->data[0])[i * ch];
				r = ch > 1 ? ((float *)f->data[0])[i * ch + 1] : l;
			} else if (f->format == AV_SAMPLE_FMT_S16) {
				l = ((int16_t *)f->data[0])[i * ch] / 32768.0f;
				r = ch > 1 ? ((int16_t *)f->data[0])[i * ch + 1] / 32768.0f : l;
			} else {
				l = r = 0;
			}
			dst[(total + i) * 2] = l;
			dst[(total + i) * 2 + 1] = r;
		}
		total += n;
		av_frame_unref(f);
	}
	return total;
}

static void eris_opus_close(AVCodecContext *c, AVFrame *f, AVPacket *p) {
	av_frame_free(&f);
	av_packet_free(&p);
	avcodec_free_context(&c);
}

static AVFrame *eris_frame(void) { return av_frame_alloc(); }
static AVPacket *eris_packet(void) { return av_packet_alloc(); }
static void eris_err(int err, char *buf, int n) { av_strerror(err, buf, n); }
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// FrameSamples is the samples per channel in one packet (10 ms at 48 kHz);
// FrameBytes is that as interleaved stereo s16.
const (
	FrameSamples = 480
	FrameBytes   = FrameSamples * 2 * 2
)

func avErr(what string, err C.int) error {
	buf := make([]byte, 128)
	C.eris_err(err, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	for i, b := range buf {
		if b == 0 {
			buf = buf[:i]
			break
		}
	}
	return fmt.Errorf("%s: %s", what, buf)
}

// Encoder turns 10 ms of PCM into Opus packets.
type Encoder struct {
	c   *C.AVCodecContext
	f   *C.AVFrame
	p   *C.AVPacket
	buf []byte
}

// NewEncoder opens a libopus encoder at kbps.
func NewEncoder(kbps int) (*Encoder, error) {
	e := &Encoder{f: C.eris_frame(), p: C.eris_packet(), buf: make([]byte, 1500)}
	if err := C.eris_opus_enc(&e.c, C.int(kbps)); err < 0 {
		e.Close()
		return nil, avErr("opening the Opus encoder", err)
	}
	return e, nil
}

// Encode takes FrameBytes of interleaved stereo s16 PCM and gives one packet
// (nil while the encoder warms up). The result is reused by the next call.
func (e *Encoder) Encode(pcm []byte) ([]byte, error) {
	if len(pcm) != FrameBytes {
		return nil, errors.New("opus: wrong frame size")
	}
	n := C.eris_opus_encode(e.c, e.f, e.p, (*C.int16_t)(unsafe.Pointer(&pcm[0])),
		(*C.uint8_t)(unsafe.Pointer(&e.buf[0])), C.int(len(e.buf)))
	if n < 0 {
		return nil, avErr("encoding sound", n)
	}
	return e.buf[:n], nil
}

// Close frees the encoder.
func (e *Encoder) Close() {
	if e.f != nil || e.p != nil || e.c != nil {
		C.eris_opus_close(e.c, e.f, e.p)
		e.c, e.f, e.p = nil, nil, nil
	}
}

// Decoder turns Opus packets back into PCM.
type Decoder struct {
	c   *C.AVCodecContext
	f   *C.AVFrame
	p   *C.AVPacket
	buf []float32
}

// NewDecoder opens FFmpeg's Opus decoder.
func NewDecoder() (*Decoder, error) {
	d := &Decoder{f: C.eris_frame(), p: C.eris_packet(), buf: make([]float32, 5760*2)}
	if err := C.eris_opus_dec(&d.c); err < 0 {
		d.Close()
		return nil, avErr("opening the Opus decoder", err)
	}
	return d, nil
}

// Decode gives a packet's sound as interleaved stereo float32 (reused by
// the next call).
func (d *Decoder) Decode(pkt []byte) ([]float32, error) {
	if len(pkt) == 0 {
		return nil, nil
	}
	n := C.eris_opus_decode(d.c, d.f, d.p, (*C.uint8_t)(unsafe.Pointer(&pkt[0])), C.int(len(pkt)),
		(*C.float)(unsafe.Pointer(&d.buf[0])), C.int(len(d.buf)/2))
	if n < 0 {
		return nil, avErr("decoding sound", n)
	}
	return d.buf[:int(n)*2], nil
}

// Close frees the decoder.
func (d *Decoder) Close() {
	if d.f != nil || d.p != nil || d.c != nil {
		C.eris_opus_close(d.c, d.f, d.p)
		d.c, d.f, d.p = nil, nil, nil
	}
}
