//go:build ignore

// One-shot generator for the AniClew tray icon. Produces aniclew.ico with two
// PNG-encoded sizes (16x16, 32x32) — Windows Vista+ accepts PNG inside ICO.
// Run with:  go run gen.go
// Stdlib only; no design assets needed.

package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
)

var (
	bg     = color.RGBA{0x1a, 0x1b, 0x26, 0xff} // deep indigo
	accent = color.RGBA{0x7a, 0xa2, 0xf7, 0xff} // bright cyan-blue
)

// makeIcon draws a stylized "A" mark scaled to the given pixel size. The
// shape is rendered geometrically (no font) so the binary keeps zero deps.
func makeIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

	// Coordinates normalized to a 32-unit grid, then scaled.
	s := func(v int) int { return v * size / 32 }

	// Outer triangle (A silhouette): apex at top center.
	fillPoly(img, accent, []point{
		{s(16), s(4)}, {s(4), s(28)}, {s(28), s(28)},
	})

	// Inner "letterhole" — fills the upper triangular notch with background
	// so we get the open-top look of an A.
	fillPoly(img, bg, []point{
		{s(16), s(12)}, {s(10), s(22)}, {s(22), s(22)},
	})

	// Crossbar of the A — short accent rectangle cutting across the hole.
	fillRect(img, accent, s(11), s(18), s(21), s(20))

	return img
}

type point struct{ x, y int }

// fillPoly is a simple scanline polygon fill (good enough for convex triangles
// at icon resolution — no anti-aliasing, which actually looks cleaner at 16px).
func fillPoly(img *image.RGBA, c color.Color, pts []point) {
	if len(pts) < 3 {
		return
	}
	minY, maxY := pts[0].y, pts[0].y
	for _, p := range pts[1:] {
		if p.y < minY {
			minY = p.y
		}
		if p.y > maxY {
			maxY = p.y
		}
	}
	for y := minY; y <= maxY; y++ {
		var xs []int
		for i := 0; i < len(pts); i++ {
			a, b := pts[i], pts[(i+1)%len(pts)]
			if (a.y <= y && b.y > y) || (b.y <= y && a.y > y) {
				// Edge crosses this scanline; compute x intercept.
				x := a.x + (y-a.y)*(b.x-a.x)/(b.y-a.y)
				xs = append(xs, x)
			}
		}
		if len(xs) < 2 {
			continue
		}
		// Even-odd fill between sorted x pairs.
		for i := 0; i+1 < len(xs); i++ {
			for j := i + 1; j < len(xs); j++ {
				if xs[j] < xs[i] {
					xs[i], xs[j] = xs[j], xs[i]
				}
			}
		}
		for k := 0; k+1 < len(xs); k += 2 {
			for x := xs[k]; x <= xs[k+1]; x++ {
				img.Set(x, y, c)
			}
		}
	}
}

func fillRect(img *image.RGBA, c color.Color, x0, y0, x1, y1 int) {
	r := image.Rect(x0, y0, x1+1, y1+1)
	draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
}

func encodePNG(img *image.RGBA) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatal(err)
	}
	return buf.Bytes()
}

// writeICO assembles an .ico containing each PNG image. ICONDIR + entries +
// data, per https://learn.microsoft.com/windows/win32/menurc/icon-resource.
func writeICO(path string, pngs map[int][]byte) error {
	// Stable size ordering so the ICO bytes are reproducible across runs.
	sizes := make([]int, 0, len(pngs))
	for s := range pngs {
		sizes = append(sizes, s)
	}
	for i := 0; i < len(sizes); i++ {
		for j := i + 1; j < len(sizes); j++ {
			if sizes[j] < sizes[i] {
				sizes[i], sizes[j] = sizes[j], sizes[i]
			}
		}
	}

	var out bytes.Buffer
	// ICONDIR header
	binary.Write(&out, binary.LittleEndian, uint16(0))            // reserved
	binary.Write(&out, binary.LittleEndian, uint16(1))            // type = icon
	binary.Write(&out, binary.LittleEndian, uint16(len(sizes)))   // count

	dirSize := 6 + 16*len(sizes)
	offset := dirSize
	for _, s := range sizes {
		data := pngs[s]
		w, h := uint8(s), uint8(s)
		if s >= 256 {
			w, h = 0, 0 // ICO convention: 0 means 256
		}
		binary.Write(&out, binary.LittleEndian, w)                 // width
		binary.Write(&out, binary.LittleEndian, h)                 // height
		binary.Write(&out, binary.LittleEndian, uint8(0))          // colors
		binary.Write(&out, binary.LittleEndian, uint8(0))          // reserved
		binary.Write(&out, binary.LittleEndian, uint16(1))         // planes
		binary.Write(&out, binary.LittleEndian, uint16(32))        // bpp
		binary.Write(&out, binary.LittleEndian, uint32(len(data))) // size
		binary.Write(&out, binary.LittleEndian, uint32(offset))    // offset
		offset += len(data)
	}
	for _, s := range sizes {
		out.Write(pngs[s])
	}
	return os.WriteFile(path, out.Bytes(), 0644)
}

func main() {
	pngs := map[int][]byte{
		16: encodePNG(makeIcon(16)),
		32: encodePNG(makeIcon(32)),
	}
	if err := writeICO("aniclew.ico", pngs); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote aniclew.ico")
}
