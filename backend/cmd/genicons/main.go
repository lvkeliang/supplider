// Command genicons generates the Tauri desktop icon set (PNG + ICO + ICNS)
// entirely offline with the Go standard library. It draws a simple brand
// mark — two stacked document cards on a blue gradient, echoing the
// product's "document-card supplier profile" concept — and emits every
// file tauri.conf.json references plus a 512px icon.png for Linux bundles.
//
// Usage (from backend/):
//
//	go run ./cmd/genicons -out ../src-tauri/icons
//
// The generated icons are committed (Tauri needs them at build time; the
// Rust toolchain runs on the packaging machine, not here). Replace with
// designer artwork later by overwriting the files with the same names.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

// Brand palette (mirrors frontend/tailwind.config.js brand-*).
var (
	cTop    = color.NRGBA{R: 37, G: 99, B: 235, A: 255} // brand-500
	cBottom = color.NRGBA{R: 30, G: 64, B: 175, A: 255} // brand-700
	cCard   = color.NRGBA{R: 29, G: 78, B: 216, A: 255} // brand-600 (lines)
	cWhite  = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
)

const ss = 4 // supersampling factor for edge anti-aliasing

func main() {
	out := flag.String("out", "../src-tauri/icons", "output directory (run from backend/)")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "genicons:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	sizes := []int{16, 32, 48, 64, 128, 256, 512}
	pngs := make(map[int][]byte, len(sizes))
	for _, s := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(s)); err != nil {
			return err
		}
		pngs[s] = buf.Bytes()
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	write := func(name string, data []byte) error {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", p)
		return nil
	}

	if err := write("32x32.png", pngs[32]); err != nil {
		return err
	}
	if err := write("128x128.png", pngs[128]); err != nil {
		return err
	}
	if err := write("128x128@2x.png", pngs[256]); err != nil {
		return err
	}
	if err := write("icon.png", pngs[512]); err != nil {
		return err
	}
	if err := write("icon.ico", ico(pngs[16], pngs[32], pngs[48], pngs[256])); err != nil {
		return err
	}
	if err := write("icon.icns", icns(pngs)); err != nil {
		return err
	}
	return nil
}

// render draws the icon at size s (px) by rasterizing at 4x and box-down-
// sampling (premultiplied) for smooth edges.
func render(s int) image.Image {
	n := s * ss
	img := image.NewNRGBA(image.Rect(0, 0, n, n))

	// Background: vertical brand gradient inside a rounded square.
	radius := n * 22 / 100
	for y := 0; y < n; y++ {
		t := float64(y) / float64(n)
		bg := mix(cTop, cBottom, t)
		for x := 0; x < n; x++ {
			if inRoundRect(x, y, 0, 0, n, n, radius) {
				img.SetNRGBA(x, y, bg)
			}
		}
	}

	// Two stacked "document cards": the back card peeks out top-right,
	// translucent white; the front card is solid white, centered.
	cw, ch := n*46/100, n*54/100
	cr := n * 7 / 100
	bx0 := n/2 + n*8/100 - cw/2
	by0 := n/2 - n*7/100 - ch/2
	fx0 := n/2 - n*3/100 - cw/2
	fy0 := n/2 + n*4/100 - ch/2

	fillRoundRect(img, bx0, by0, bx0+cw, by0+ch, cr, color.NRGBA{R: 255, G: 255, B: 255, A: 110})
	fillRoundRect(img, fx0, fy0, fx0+cw, fy0+ch, cr, cWhite)

	// Three brand-colored "text lines" on the front card.
	lh := n * 9 / 200
	lr := lh / 2
	lx0 := fx0 + n*8/100
	for i, w := range []int{n * 26 / 100, n * 21 / 100, n * 15 / 100} {
		ly0 := fy0 + ch*(28+20*i)/100
		fillRoundRect(img, lx0, ly0, lx0+w, ly0+lh, lr, cCard)
	}

	return downsample(img, ss)
}

// inRoundRect reports whether (x,y) is inside the rounded rectangle
// [x0,x1)×[y0,y1) with corner radius r (integer grid).
func inRoundRect(x, y, x0, y0, x1, y1, r int) bool {
	if x < x0 || x >= x1 || y < y0 || y >= y1 {
		return false
	}
	dx, dy := 0, 0
	if cx := x0 + r; x < cx {
		dx = cx - x
	} else if cx := x1 - 1 - r; x > cx {
		dx = x - cx
	}
	if cy := y0 + r; y < cy {
		dy = cy - y
	} else if cy := y1 - 1 - r; y > cy {
		dy = y - cy
	}
	if dx == 0 && dy == 0 {
		return true
	}
	return dx*dx+dy*dy <= r*r
}

// fillRoundRect blends src (straight alpha) over the image.
func fillRoundRect(img *image.NRGBA, x0, y0, x1, y1, r int, src color.NRGBA) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if !inRoundRect(x, y, x0, y0, x1, y1, r) {
				continue
			}
			i := img.PixOffset(x, y)
			img.Pix[i+0] = blendByte(img.Pix[i+0], src.R, src.A)
			img.Pix[i+1] = blendByte(img.Pix[i+1], src.G, src.A)
			img.Pix[i+2] = blendByte(img.Pix[i+2], src.B, src.A)
			sa := int(src.A)
			da := int(img.Pix[i+3])
			img.Pix[i+3] = byte(sa + da*(255-sa)/255)
		}
	}
}

// blendByte composites src over dst for one channel (straight alpha).
func blendByte(dst, src, sa byte) byte {
	s, d, a := int(src), int(dst), int(sa)
	return byte((s*a + d*(255-a)) / 255)
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		R: byte(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: byte(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: byte(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 255,
	}
}

// downsample box-filters a supersampled image back to n/k resolution,
// averaging in premultiplied alpha space so transparent corners stay clean.
func downsample(src *image.NRGBA, k int) image.Image {
	n := src.Bounds().Dx() / k
	dst := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var pr, pg, pb, pa float64
			for sy := 0; sy < k; sy++ {
				for sx := 0; sx < k; sx++ {
					c := src.NRGBAAt(x*k+sx, y*k+sy)
					a := float64(c.A) / 255
					pr += float64(c.R) * a
					pg += float64(c.G) * a
					pb += float64(c.B) * a
					pa += a
				}
			}
			m := float64(k * k)
			pa /= m
			oi := dst.PixOffset(x, y)
			dst.Pix[oi+3] = byte(math.Round(pa * 255))
			if pa > 0 {
				dst.Pix[oi+0] = byte(math.Round(pr / m / pa))
				dst.Pix[oi+1] = byte(math.Round(pg / m / pa))
				dst.Pix[oi+2] = byte(math.Round(pb / m / pa))
			}
		}
	}
	return dst
}

// ico builds a Windows .ico with PNG-compressed images (Vista+; Tauri
// targets Windows 10+), one entry per supplied PNG in ascending size order.
func ico(pngs ...[]byte) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // type: icon
	binary.Write(&buf, binary.LittleEndian, uint16(len(pngs)))

	offset := 6 + 16*len(pngs)
	// Sizes here must match the rendered PNGs (16,32,48,256).
	for _, dim := range []int{16, 32, 48, 256} {
		var data []byte
		for i, s := range []int{16, 32, 48, 256} {
			if s == dim {
				data = pngs[i]
			}
		}
		w := byte(dim)
		if dim == 256 {
			w = 0 // 0 means 256
		}
		buf.WriteByte(w)
		buf.WriteByte(w)
		buf.WriteByte(0)                                    // color count (0 = true color)
		buf.WriteByte(0)                                    // reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
		binary.Write(&buf, binary.LittleEndian, uint16(32)) // bits per pixel
		binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
		binary.Write(&buf, binary.LittleEndian, uint32(offset))
		offset += len(data)
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return buf.Bytes()
}

// icns builds a macOS .icns container with PNG-compressed element types
// covering 16–512px (incl. @2x retina slots).
func icns(pngs map[int][]byte) []byte {
	type elem struct {
		typ  string
		size int
	}
	elems := []elem{
		{"icp4", 16}, // 16x16
		{"icp5", 32}, // 32x32
		{"ic12", 64}, // 32@2x
		{"ic07", 128},
		{"ic08", 256},
		{"ic13", 256}, // 128@2x
		{"ic09", 512},
		{"ic14", 512}, // 256@2x
	}
	var body bytes.Buffer
	for _, e := range elems {
		data := pngs[e.size]
		body.WriteString(e.typ)
		binary.Write(&body, binary.BigEndian, uint32(8+len(data)))
		body.Write(data)
	}
	var out bytes.Buffer
	out.WriteString("icns")
	binary.Write(&out, binary.BigEndian, uint32(8+body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}
