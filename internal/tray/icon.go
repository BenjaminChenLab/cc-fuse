package tray

// ICO 製作:32x32 圓形色點 → PNG → 包進 ICO 容器(Vista+ 支援 PNG-in-ICO)。
// 不依賴任何圖檔資源,exe 單檔自足(設計 §10)。

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// iconSize is the tray icon's square pixel size (standard small tray size).
const iconSize = 32

// iconRadius / iconRing: the filled dot and its darker outline band.
const (
	iconRadius = 13
	iconRing   = 11
)

// stateColors maps each tray state to its fill + outline color (design §8
// color semantics).
var stateColors = map[IconState][2]color.RGBA{
	IconGreenA:  {{R: 0x2E, G: 0xA0, B: 0x43, A: 0xFF}, {R: 0x1B, G: 0x5E, B: 0x27, A: 0xFF}},
	IconOrangeB: {{R: 0xE8, G: 0x8A, B: 0x00, A: 0xFF}, {R: 0x8A, G: 0x52, B: 0x00, A: 0xFF}},
	IconGray:    {{R: 0x6E, G: 0x77, B: 0x81, A: 0xFF}, {R: 0x3E, G: 0x45, B: 0x4C, A: 0xFF}},
	IconRed:     {{R: 0xD8, G: 0x36, B: 0x33, A: 0xFF}, {R: 0x7E, G: 0x1D, B: 0x1B, A: 0xFF}},
}

// IconBytes renders one tray icon as ICO bytes: a solid dot in the state's
// color with a darker outline on a transparent background. Deterministic —
// the same state always yields the same bytes.
func IconBytes(s IconState) ([]byte, error) {
	colors, ok := stateColors[s]
	if !ok {
		colors = stateColors[IconGray]
	}
	return icoFromPNG(drawDot(colors[0], colors[1]))
}

// drawDot renders the 32x32 RGBA dot image.
func drawDot(fill, ring color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	c := float64(iconSize) / 2
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			d2 := (float64(x)+0.5-c)*(float64(x)+0.5-c) + (float64(y)+0.5-c)*(float64(y)+0.5-c)
			switch {
			case d2 <= iconRing*iconRing:
				img.Set(x, y, ring)
			case d2 <= iconRadius*iconRadius:
				img.Set(x, y, fill)
			}
		}
	}
	return img
}

// icoDirSize is the 6-byte ICONDIR plus one 16-byte ICONDIRENTRY.
const icoDirSize = 22

// icoFromPNG encodes img as PNG and wraps it in a single-image ICO container.
func icoFromPNG(img image.Image) ([]byte, error) {
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return nil, err
	}
	out := make([]byte, icoDirSize, icoDirSize+pngBuf.Len())
	// ICONDIR:reserved=0, type=1(icon), count=1。
	out[0], out[1] = 0, 0
	out[2], out[3] = 1, 0
	out[4], out[5] = 1, 0
	// ICONDIRENTRY:寬高 32(非 0 值直接寫)、色板數 0、保留 0、色面 1、
	// 位元深度 32、資料長度、資料偏移 22。
	out[6] = iconSize
	out[7] = iconSize
	out[8], out[9] = 0, 0
	out[10], out[11] = 1, 0
	out[12], out[13] = 32, 0
	w := pngBuf.Len()
	out[14], out[15] = byte(w), byte(w>>8)
	out[16], out[17] = byte(w>>16), byte(w>>24)
	out[18], out[19] = icoDirSize, 0
	out[20], out[21] = 0, 0
	return append(out, pngBuf.Bytes()...), nil
}
