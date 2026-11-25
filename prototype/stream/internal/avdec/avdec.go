// Package avdec decodes H.264 inside the program with FFmpeg's libraries,
// using the graphics card (NVDEC through CUDA, or VA-API) when available.
// Each frame goes in and the picture comes out in the same call: no queues.
package avdec

/*
#cgo pkg-config: libavcodec libavutil
#include <errno.h>
#include <stdlib.h>
#include <string.h>
#include <libavcodec/avcodec.h>
#include <libavutil/hwcontext.h>

// One decoder per process, so the chosen hardware format can live here.
static enum AVPixelFormat eris_hw_fmt = AV_PIX_FMT_NONE;

static enum AVPixelFormat eris_get_format(AVCodecContext *c, const enum AVPixelFormat *fmts) {
	for (const enum AVPixelFormat *p = fmts; *p != AV_PIX_FMT_NONE; p++)
		if (*p == eris_hw_fmt) return *p;
	return fmts[0]; // no hardware: software decoding
}

static int eris_open(AVCodecContext **out, const char *hw) {
	const AVCodec *codec = avcodec_find_decoder(AV_CODEC_ID_H264);
	if (!codec) return AVERROR_DECODER_NOT_FOUND;
	AVCodecContext *c = avcodec_alloc_context3(codec);
	if (!c) return AVERROR(ENOMEM);
	c->flags |= AV_CODEC_FLAG_LOW_DELAY; // output each frame immediately
	c->thread_count = 1;                 // frame threads would hold frames back

	enum AVHWDeviceType type = hw && *hw ? av_hwdevice_find_type_by_name(hw) : AV_HWDEVICE_TYPE_NONE;
	if (type != AV_HWDEVICE_TYPE_NONE) {
		for (int i = 0;; i++) {
			const AVCodecHWConfig *cfg = avcodec_get_hw_config(codec, i);
			if (!cfg) break;
			if ((cfg->methods & AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX) && cfg->device_type == type) {
				eris_hw_fmt = cfg->pix_fmt;
				break;
			}
		}
		if (eris_hw_fmt != AV_PIX_FMT_NONE &&
		    av_hwdevice_ctx_create(&c->hw_device_ctx, type, NULL, NULL, 0) == 0) {
			c->get_format = eris_get_format;
		} else {
			eris_hw_fmt = AV_PIX_FMT_NONE;
		}
	}
	int err = avcodec_open2(c, codec, NULL);
	if (err < 0) { avcodec_free_context(&c); return err; }
	*out = c;
	return 0;
}

static int eris_send(AVCodecContext *c, AVPacket *pkt, const uint8_t *data, int size) {
	int err = av_new_packet(pkt, size);
	if (err < 0) return err;
	memcpy(pkt->data, data, size);
	err = avcodec_send_packet(c, pkt);
	av_packet_unref(pkt);
	return err;
}

// Returns 1 with a frame in sw (NV12 or YUV420P), 0 when no frame is ready, <0 on error.
static int eris_receive(AVCodecContext *c, AVFrame *f, AVFrame *sw) {
	int err = avcodec_receive_frame(c, f);
	if (err == AVERROR(EAGAIN) || err == AVERROR_EOF) return 0;
	if (err < 0) return err;
	av_frame_unref(sw);
	if (f->format == eris_hw_fmt) {
		err = av_hwframe_transfer_data(sw, f, 0); // graphics card -> memory, as NV12
		av_frame_unref(f);
		if (err < 0) return err;
	} else {
		av_frame_move_ref(sw, f);
	}
	return 1;
}

static int eris_is_hw(void) { return eris_hw_fmt != AV_PIX_FMT_NONE; }
static int eris_fmt_nv12(void) { return AV_PIX_FMT_NV12; }
static int eris_fmt_yuv420p(void) { return AV_PIX_FMT_YUV420P; }
static uint8_t *eris_plane(AVFrame *f, int i) { return f->data[i]; }
static int eris_linesize(AVFrame *f, int i) { return f->linesize[i]; }
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Decoder is one H.264 decoder.
type Decoder struct {
	ctx      *C.AVCodecContext
	pkt      *C.AVPacket
	frame    *C.AVFrame
	sw       *C.AVFrame
	Hardware bool
}

func averr(code C.int) error {
	var buf [128]C.char
	C.av_strerror(code, &buf[0], C.size_t(len(buf)))
	return errors.New(C.GoString(&buf[0]))
}

// New opens a decoder. hw is an FFmpeg device type ("cuda", "vaapi") or ""
// for software. If the hardware can't be opened, it decodes in software.
func New(hw string) (*Decoder, error) {
	var ctx *C.AVCodecContext
	chw := C.CString(hw)
	defer C.free(unsafe.Pointer(chw))
	if err := C.eris_open(&ctx, chw); err < 0 {
		return nil, fmt.Errorf("open decoder: %w", averr(err))
	}
	return &Decoder{
		ctx: ctx, pkt: C.av_packet_alloc(), frame: C.av_frame_alloc(), sw: C.av_frame_alloc(),
		Hardware: C.eris_is_hw() != 0,
	}, nil
}

// Decode decodes one complete frame (Annex-B access unit) and writes the
// picture as NV12 with stride w into dst (len w*h*3/2). It returns false when
// the decoder produced no picture (for example, before the first keyframe).
func (d *Decoder) Decode(au []byte, dst []byte, w, h int) (bool, error) {
	if len(au) == 0 {
		return false, nil
	}
	if err := C.eris_send(d.ctx, d.pkt, (*C.uint8_t)(unsafe.Pointer(&au[0])), C.int(len(au))); err < 0 {
		return false, fmt.Errorf("decode: %w", averr(err))
	}
	got := false
	for {
		r := C.eris_receive(d.ctx, d.frame, d.sw)
		if r < 0 {
			return got, fmt.Errorf("decode: %w", averr(r))
		}
		if r == 0 {
			return got, nil
		}
		if int(d.sw.width) != w || int(d.sw.height) != h {
			return got, fmt.Errorf("decode: picture is %dx%d, expected %dx%d", d.sw.width, d.sw.height, w, h)
		}
		d.copyNV12(dst, w, h)
		got = true
	}
}

func plane(f *C.AVFrame, i, rows int) ([]byte, int) {
	ls := int(C.eris_linesize(f, C.int(i)))
	return unsafe.Slice((*byte)(unsafe.Pointer(C.eris_plane(f, C.int(i)))), ls*rows), ls
}

func (d *Decoder) copyNV12(dst []byte, w, h int) {
	y, ys := plane(d.sw, 0, h)
	for r := 0; r < h; r++ {
		copy(dst[r*w:r*w+w], y[r*ys:r*ys+w])
	}
	uvDst := dst[w*h:]
	switch int(d.sw.format) {
	case int(C.eris_fmt_nv12()):
		uv, uvs := plane(d.sw, 1, h/2)
		for r := 0; r < h/2; r++ {
			copy(uvDst[r*w:r*w+w], uv[r*uvs:r*uvs+w])
		}
	case int(C.eris_fmt_yuv420p()): // software decoding: interleave U and V
		u, us := plane(d.sw, 1, h/2)
		v, vs := plane(d.sw, 2, h/2)
		for r := 0; r < h/2; r++ {
			row := uvDst[r*w : r*w+w]
			for c := 0; c < w/2; c++ {
				row[2*c] = u[r*us+c]
				row[2*c+1] = v[r*vs+c]
			}
		}
	}
}

// Close frees the decoder.
func (d *Decoder) Close() {
	C.av_frame_free(&d.frame)
	C.av_frame_free(&d.sw)
	C.av_packet_free(&d.pkt)
	C.avcodec_free_context(&d.ctx)
}
