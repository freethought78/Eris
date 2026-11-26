// ptclock draws the current time as a barcode in the top-left corner of the
// host's screen, so the player can measure true screen-to-screen delay. With
// -busy it also fills its window with moving content, so the encoder has real
// work to do (a still desktop compresses to almost nothing).
package main

import (
	"flag"
	"log"
	"runtime"
	"time"
	"unsafe"

	"github.com/freethought78/eris/prototype/stream/internal/mono"
	"github.com/freethought78/eris/prototype/stream/internal/stamp"
	"github.com/veandco/go-sdl2/sdl"
)

func init() { runtime.LockOSThread() }

var (
	x        = flag.Int("x", 0, "window x (the barcode must sit at the capture region's top-left)")
	y        = flag.Int("y", 0, "window y")
	w        = flag.Int("w", stamp.Bits*stamp.Block, "window width")
	h        = flag.Int("h", stamp.Block, "window height")
	busy     = flag.Bool("busy", false, "fill the window with moving content")
	fps      = flag.Int("fps", 60, "redraw rate")
	top      = flag.Bool("top", false, "keep the window above others (for use over a game)")
	pattern  = flag.String("pattern", "sharp", "with -busy: sharp (a hard-edged scrolling pattern NVENC can't squeeze below about 8 Mbps at 1440p) or shapes (coloured blocks moving over a gradient, easy to compress like most games)")
	noiseArg = flag.Int("noise", 512, "with -busy: size of the square of random noise redrawn every frame (it can't be compressed; 512 needs about 13 Mbps at 2560x1440)")
)

var noiseSize int32

func main() {
	flag.Parse()
	noiseSize = int32(max(*noiseArg, 8))
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		log.Fatal(err)
	}
	defer sdl.Quit()
	flags := uint32(sdl.WINDOW_SHOWN | sdl.WINDOW_BORDERLESS)
	if *top {
		flags |= sdl.WINDOW_ALWAYS_ON_TOP
	}
	win, err := sdl.CreateWindow("ptclock", int32(*x), int32(*y), int32(*w), int32(*h), flags)
	if err != nil {
		log.Fatal(err)
	}
	defer win.Destroy()
	ren, err := sdl.CreateRenderer(win, -1, 0)
	if err != nil {
		log.Fatal(err)
	}
	defer ren.Destroy()

	var bg, noise *sdl.Texture
	var noisePix []byte
	if *busy {
		// A wide colour pattern, scrolled every frame: whole-screen motion.
		bw, bh := *w*2, *h
		pix := make([]byte, bw*bh*4)
		for j := 0; j < bh; j++ {
			for i := 0; i < bw; i++ {
				o := (j*bw + i) * 4
				if *pattern == "shapes" {
					pix[o] = byte(j * 160 / bh)
					pix[o+1] = byte(40 + j*80/bh)
					pix[o+2] = byte(200 - j*120/bh)
				} else {
					pix[o] = byte(i * 3)
					pix[o+1] = byte(j*2 + i/7)
					pix[o+2] = byte((i ^ j) * 5)
				}
				pix[o+3] = 255
			}
		}
		if bg, err = ren.CreateTexture(sdl.PIXELFORMAT_ABGR8888, sdl.TEXTUREACCESS_STATIC, int32(bw), int32(bh)); err != nil {
			log.Fatal(err)
		}
		_ = bg.Update(nil, unsafe.Pointer(&pix[0]), bw*4)
		// A noise patch, new every frame: detail the encoder can't predict.
		if noise, err = ren.CreateTexture(sdl.PIXELFORMAT_ABGR8888, sdl.TEXTUREACCESS_STREAMING, noiseSize, noiseSize); err != nil {
			log.Fatal(err)
		}
		noisePix = make([]byte, int(noiseSize)*int(noiseSize)*4)
	}

	tick := time.NewTicker(time.Second / time.Duration(*fps))
	defer tick.Stop()
	var seed uint64 = 88172645463325252
	for n := 0; ; n++ {
		for ev := sdl.PollEvent(); ev != nil; ev = sdl.PollEvent() {
			if _, ok := ev.(*sdl.QuitEvent); ok {
				return
			}
		}
		ren.SetDrawColor(0, 0, 0, 255)
		ren.Clear()
		if *busy {
			off := int32((n * 8) % *w)
			_ = ren.Copy(bg, &sdl.Rect{X: off, Y: 0, W: int32(*w), H: int32(*h)}, nil)
			if *pattern == "shapes" {
				for k := 0; k < 40; k++ {
					// Blocks moving at different speeds and directions.
					sx, sy := (k%7)-3, (k%5)-2
					bx := (k*397 + n*sx*(2+k%4)) % (*w - 160)
					by := (k*211 + n*sy*(1+k%3)) % (*h - 120)
					if bx < 0 {
						bx += *w - 160
					}
					if by < 0 {
						by += *h - 120
					}
					ren.SetDrawColor(uint8(k*61), uint8(k*37+90), uint8(k*23+40), 255)
					_ = ren.FillRect(&sdl.Rect{X: int32(bx), Y: int32(by), W: int32(80 + k%5*20), H: int32(60 + k%4*15)})
				}
			}
			for i := 0; i+8 <= len(noisePix); i += 8 {
				seed ^= seed << 13
				seed ^= seed >> 7
				seed ^= seed << 17
				*(*uint64)(unsafe.Pointer(&noisePix[i])) = seed | 0xff000000ff000000
			}
			_ = noise.Update(nil, unsafe.Pointer(&noisePix[0]), int(noiseSize)*4)
			nx := int32(*w/2) - noiseSize/2 + int32((n*5)%200)
			_ = ren.Copy(noise, nil, &sdl.Rect{X: nx, Y: int32(*h/2) - noiseSize/2, W: noiseSize, H: noiseSize})
		}
		// The barcode, drawn last and stamped as late as possible.
		word := stamp.Encode(mono.Now())
		for i := 0; i < stamp.Bits; i++ {
			if word&(1<<(stamp.Bits-1-i)) != 0 {
				ren.SetDrawColor(255, 255, 255, 255)
			} else {
				ren.SetDrawColor(0, 0, 0, 255)
			}
			_ = ren.FillRect(&sdl.Rect{X: int32(i * stamp.Block), Y: 0, W: stamp.Block, H: stamp.Block})
		}
		ren.Present()
		<-tick.C
	}
}
