// genicon writes the app icon (PNG-in-ICO, several sizes) to the given paths.
//
//	go run ./tools/genicon internal/tray/icon.ico installer/focusguard.ico
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func draw(size int) []byte {
	const ss = 4 // supersampling for smooth edges
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	c := float64(size) / 2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss - c
					py := float64(y) + (float64(sy)+0.5)/ss - c
					d := math.Hypot(px, py) / c
					// rounded-square backdrop
					qx, qy := math.Abs(px)/c-0.78, math.Abs(py)/c-0.78
					in := math.Hypot(math.Max(qx, 0), math.Max(qy, 0))+math.Min(math.Max(qx, qy), 0) < 0.22
					if !in {
						continue
					}
					cr, cg, cb := 15.0, 20.0, 32.0
					if d > 0.40 && d < 0.66 { // gradient ring
						t := (math.Atan2(py, px) + math.Pi) / (2 * math.Pi)
						cr, cg, cb = 108+52*math.Sin(t*2*math.Pi), 140-40*math.Sin(t*2*math.Pi), 255
					}
					r, g, b, a = r+cr, g+cg, b+cb, a+1
				}
			}
			if a > 0 {
				n := float64(ss * ss)
				img.SetNRGBA(x, y, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(255 * a / n)})
			}
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func main() {
	sizes := []int{16, 32, 48, 256}
	var imgs [][]byte
	for _, s := range sizes {
		imgs = append(imgs, draw(s))
	}
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, s := range sizes {
		w := byte(s)
		if s == 256 {
			w = 0
		}
		out.Write([]byte{w, w, 0, 0})
		binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&out, binary.LittleEndian, [2]uint32{uint32(len(imgs[i])), uint32(off)})
		off += len(imgs[i])
	}
	for _, b := range imgs {
		out.Write(b)
	}
	for _, p := range os.Args[1:] {
		if err := os.WriteFile(p, out.Bytes(), 0o644); err != nil {
			panic(err)
		}
	}
}
