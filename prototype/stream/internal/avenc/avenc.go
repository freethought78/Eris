// Package avenc captures the screen and hardware-encodes it inside the
// program with FFmpeg's libraries: x11grab for capture, NVENC for encoding.
// Unlike the ffmpeg program, it can change the bitrate between frames and
// make a keyframe on request. When the output is smaller than the capture
// (the quality ladder), the picture is uploaded to the graphics card and
// scaled there (hwupload_cuda, scale_cuda) before NVENC, so a smaller picture
// costs no extra copying or CPU time.
package avenc

/*
#cgo pkg-config: libavdevice libavformat libavfilter libavcodec libavutil
#include <stdlib.h>
#include <libavdevice/avdevice.h>
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/imgutils.h>
#include <libavutil/opt.h>
#include <libavutil/hwcontext.h>
#include <libavfilter/avfilter.h>
#include <libavfilter/buffersrc.h>
#include <libavfilter/buffersink.h>

typedef struct {
	AVFormatContext *in;
	AVCodecContext *enc;
	AVPacket *cap;
	AVPacket *out;
	AVFrame *frame;
	int64_t n;
	int want_key;
	// Scaling on the graphics card (only when the output is smaller).
	AVBufferRef *dev;
	AVFilterGraph *graph;
	AVFilterContext *src, *sink;
	AVFrame *scaled;
} eris_enc;

// Builds capture (bgr0) -> hwupload_cuda -> scale_cuda=w:h -> sink.
static int eris_open_scaler(eris_enc *e, int cw, int ch, int w, int h, int fps) {
	int err = av_hwdevice_ctx_create(&e->dev, AV_HWDEVICE_TYPE_CUDA, NULL, NULL, 0);
	if (err < 0) return err;
	e->graph = avfilter_graph_alloc();
	if (!e->graph) return AVERROR(ENOMEM);
	char args[256];
	snprintf(args, sizeof args, "video_size=%dx%d:pix_fmt=%d:time_base=1/%d:pixel_aspect=1/1",
	         cw, ch, e->in->streams[0]->codecpar->format, fps);
	if ((err = avfilter_graph_create_filter(&e->src, avfilter_get_by_name("buffer"), "in", args, NULL, e->graph)) < 0) return err;
	if ((err = avfilter_graph_create_filter(&e->sink, avfilter_get_by_name("buffersink"), "out", NULL, NULL, e->graph)) < 0) return err;
	AVFilterContext *up, *scale;
	if ((err = avfilter_graph_create_filter(&up, avfilter_get_by_name("hwupload_cuda"), "up", NULL, NULL, e->graph)) < 0) return err;
	up->hw_device_ctx = av_buffer_ref(e->dev);
	snprintf(args, sizeof args, "w=%d:h=%d", w, h);
	if ((err = avfilter_graph_create_filter(&scale, avfilter_get_by_name("scale_cuda"), "scale", args, NULL, e->graph)) < 0) return err;
	if ((err = avfilter_link(e->src, 0, up, 0)) < 0) return err;
	if ((err = avfilter_link(up, 0, scale, 0)) < 0) return err;
	if ((err = avfilter_link(scale, 0, e->sink, 0)) < 0) return err;
	if ((err = avfilter_graph_config(e->graph, NULL)) < 0) return err;
	e->scaled = av_frame_alloc();
	return 0;
}

static void eris_verbose(void) { av_log_set_level(AV_LOG_VERBOSE); }

static int eris_open_capture(eris_enc *e, const char *url, const char *size, const char *fps) {
	avdevice_register_all();
	const AVInputFormat *fmt = av_find_input_format("x11grab");
	if (!fmt) return AVERROR_DEMUXER_NOT_FOUND;
	AVDictionary *o = NULL;
	av_dict_set(&o, "framerate", fps, 0);
	av_dict_set(&o, "video_size", size, 0);
	av_dict_set(&o, "draw_mouse", "0", 0);
	int err = avformat_open_input(&e->in, url, fmt, &o);
	av_dict_free(&o);
	return err;
}

static int eris_capture_format(eris_enc *e) { return e->in->streams[0]->codecpar->format; }

static int eris_open_encoder(eris_enc *e, const char *name, int w, int h, int fps, int kbps) {
	const AVCodec *codec = avcodec_find_encoder_by_name(name);
	if (!codec) return AVERROR_ENCODER_NOT_FOUND;
	AVCodecContext *c = avcodec_alloc_context3(codec);
	c->width = w;
	c->height = h;
	if (e->sink) { // scaled on the graphics card: NVENC reads the CUDA frames there
		c->pix_fmt = AV_PIX_FMT_CUDA;
		c->sw_pix_fmt = eris_capture_format(e);
		c->hw_frames_ctx = av_buffer_ref(av_buffersink_get_hw_frames_ctx(e->sink));
	} else {
		c->pix_fmt = eris_capture_format(e); // NVENC takes the captured BGR0 directly
	}
	c->time_base = (AVRational){1, fps};
	c->framerate = (AVRational){fps, 1};
	c->bit_rate = (int64_t)kbps * 1000;
	c->rc_max_rate = c->bit_rate;
	c->rc_buffer_size = (int)(c->bit_rate / fps); // about one frame
	c->gop_size = fps;                             // intra-refresh period
	c->max_b_frames = 0;
	// Each frame in slices (ERIS_SLICES, default 4), so a player that decodes
	// in software can decode them on several cores at once, with no added
	// delay (unlike decoding several frames at once). Costs a little
	// compression.
	const char *sl = getenv("ERIS_SLICES");
	c->slices = sl ? atoi(sl) : 4;
	AVDictionary *o = NULL;
	av_dict_set(&o, "preset", "p1", 0);
	av_dict_set(&o, "tune", "ull", 0);
	av_dict_set(&o, "rc", "cbr", 0);
	av_dict_set(&o, "profile", "high", 0);
	av_dict_set(&o, "zerolatency", "1", 0);
	av_dict_set(&o, "delay", "0", 0);
	av_dict_set(&o, "rc-lookahead", "0", 0);
	av_dict_set(&o, "intra-refresh", "1", 0);
	av_dict_set(&o, "forced-idr", "1", 0);
	int err = avcodec_open2(c, codec, &o);
	av_dict_free(&o);
	if (err < 0) { avcodec_free_context(&c); return err; }
	e->enc = c;
	e->cap = av_packet_alloc();
	e->out = av_packet_alloc();
	e->frame = av_frame_alloc();
	return 0;
}

// Waits for the next captured screen. Returns 0 or an error.
static int eris_grab(eris_enc *e) {
	av_packet_unref(e->cap);
	return av_read_frame(e->in, e->cap);
}

// Encodes the last grab. Returns 1 with a packet in e->out, 0 if the encoder
// gave nothing yet, <0 on error.
static int eris_encode(eris_enc *e, int cw, int ch) {
	AVFrame *f = e->frame;
	AVCodecContext *c = e->enc;
	av_frame_unref(f);
	int capfmt = eris_capture_format(e);
	f->format = capfmt;
	f->width = cw;
	f->height = ch;
	int err = av_image_fill_arrays(f->data, f->linesize, e->cap->data, capfmt, cw, ch, 1);
	if (err < 0) return err;
	f->pts = e->n++;
	if (e->sink) {
		// The capture's own buffer, shared rather than copied, then up to the
		// graphics card and scaled there.
		f->buf[0] = av_buffer_ref(e->cap->buf);
		if ((err = av_buffersrc_add_frame_flags(e->src, f, AV_BUFFERSRC_FLAG_KEEP_REF)) < 0) return err;
		av_frame_unref(e->scaled);
		if ((err = av_buffersink_get_frame(e->sink, e->scaled)) < 0) return err;
		f = e->scaled;
	}
	if (e->want_key) { f->pict_type = AV_PICTURE_TYPE_I; e->want_key = 0; }
	err = avcodec_send_frame(c, f);
	if (err < 0) return err;
	av_packet_unref(e->out);
	err = avcodec_receive_packet(c, e->out);
	if (err == AVERROR(EAGAIN)) return 0;
	return err < 0 ? err : 1;
}

static void eris_set_kbps(eris_enc *e, int kbps, int fps) {
	// NVENC applies a changed bitrate at the next frame.
	e->enc->bit_rate = (int64_t)kbps * 1000;
	e->enc->rc_max_rate = e->enc->bit_rate;
	e->enc->rc_buffer_size = (int)(e->enc->bit_rate / fps);
}

static void eris_close(eris_enc *e) {
	if (e->enc) avcodec_free_context(&e->enc);
	if (e->graph) avfilter_graph_free(&e->graph);
	if (e->scaled) av_frame_free(&e->scaled);
	if (e->dev) av_buffer_unref(&e->dev);
	if (e->in) avformat_close_input(&e->in);
	av_packet_free(&e->cap);
	av_packet_free(&e->out);
	av_frame_free(&e->frame);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"
)

// Encoder captures and encodes one screen region.
type Encoder struct {
	e          *C.eris_enc
	fps        int
	capW, capH int          // the captured region
	OutW, OutH int          // the encoded picture
	pending    atomic.Int64 // bitrate to apply before the next frame, 0 = none
}

func averr(code C.int) error {
	var buf [128]C.char
	C.av_strerror(code, &buf[0], C.size_t(len(buf)))
	return errors.New(C.GoString(&buf[0]))
}

// New opens capture of display (":0") at the region (w x h at x,y) and an
// encoder for it, encoding at outW x outH (scaled on the graphics card when
// smaller; 0 = the region's size).
func New(display string, w, h, x, y, outW, outH, fps, kbps int, encoder string) (*Encoder, error) {
	if outW <= 0 || outH <= 0 {
		outW, outH = w, h
	}
	if os.Getenv("ERIS_AVLOG") == "verbose" {
		C.eris_verbose()
	}
	e := (*C.eris_enc)(C.calloc(1, C.size_t(unsafe.Sizeof(C.eris_enc{}))))
	url := C.CString(fmt.Sprintf("%s+%d,%d", display, x, y))
	size := C.CString(fmt.Sprintf("%dx%d", w, h))
	cfps := C.CString(fmt.Sprint(fps))
	name := C.CString(encoder)
	defer func() {
		for _, p := range []*C.char{url, size, cfps, name} {
			C.free(unsafe.Pointer(p))
		}
	}()
	if err := C.eris_open_capture(e, url, size, cfps); err < 0 {
		C.eris_close(e)
		C.free(unsafe.Pointer(e))
		return nil, fmt.Errorf("open capture: %w", averr(err))
	}
	if outW != w || outH != h {
		if err := C.eris_open_scaler(e, C.int(w), C.int(h), C.int(outW), C.int(outH), C.int(fps)); err < 0 {
			C.eris_close(e)
			C.free(unsafe.Pointer(e))
			return nil, fmt.Errorf("scaling on the graphics card: %w", averr(err))
		}
	}
	if err := C.eris_open_encoder(e, name, C.int(outW), C.int(outH), C.int(fps), C.int(kbps)); err < 0 {
		C.eris_close(e)
		C.free(unsafe.Pointer(e))
		return nil, fmt.Errorf("open %s: %w", encoder, averr(err))
	}
	return &Encoder{e: e, fps: fps, capW: w, capH: h, OutW: outW, OutH: outH}, nil
}

// Grab waits for the next captured screen.
func (enc *Encoder) Grab() error {
	if err := C.eris_grab(enc.e); err < 0 {
		return fmt.Errorf("capture: %w", averr(err))
	}
	return nil
}

// Encode encodes the last grab and returns the frame as Annex-B. The returned
// slice is only valid until the next call. key is true for a keyframe.
func (enc *Encoder) Encode() (au []byte, key bool, err error) {
	if k := enc.pending.Swap(0); k > 0 {
		C.eris_set_kbps(enc.e, C.int(k), C.int(enc.fps))
	}
	r := C.eris_encode(enc.e, C.int(enc.capW), C.int(enc.capH))
	if r < 0 {
		return nil, false, fmt.Errorf("encode: %w", averr(r))
	}
	if r == 0 {
		return nil, false, nil
	}
	p := enc.e.out
	return unsafe.Slice((*byte)(unsafe.Pointer(p.data)), int(p.size)), p.flags&C.AV_PKT_FLAG_KEY != 0, nil
}

// SetKbps changes the bitrate from the next frame. Safe from any goroutine.
// Through FFmpeg, NVENC makes that frame a keyframe; with the small rate
// buffer it stays the size of a normal frame, so it costs a moment of
// sharpness, not a burst of data.
func (enc *Encoder) SetKbps(kbps int) { enc.pending.Store(int64(kbps)) }

// RequestKeyframe makes the next frame a keyframe (for a new viewer, or after
// heavy loss).
func (enc *Encoder) RequestKeyframe() { enc.e.want_key = 1 }

// Close stops capture and frees the encoder.
func (enc *Encoder) Close() {
	C.eris_close(enc.e)
	C.free(unsafe.Pointer(enc.e))
}
