package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

//go:embed kimi.ico
var kimiIcoBytes []byte

//go:embed kimi.png
var kimiPngBytes []byte

const (
	spinnerFrameCount = 8
	orbitColor        = 0x3b82f6 // 环绕小圆点(蓝)
)

// pngToIco 把 PNG 包成 ICO 容器(Windows Vista+ 支持 PNG 压缩图标)
func pngToIco(pngBytes []byte, w, h int) []byte {
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint16(0)) // reserved
	_ = binary.Write(buf, binary.LittleEndian, uint16(1)) // type: icon
	_ = binary.Write(buf, binary.LittleEndian, uint16(1)) // count
	buf.WriteByte(byte(w))
	buf.WriteByte(byte(h))
	buf.WriteByte(0) // colors
	buf.WriteByte(0) // reserved
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))  // planes
	_ = binary.Write(buf, binary.LittleEndian, uint16(32)) // bitcount
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(pngBytes)))
	_ = binary.Write(buf, binary.LittleEndian, uint32(22)) // offset = 6+16
	buf.Write(pngBytes)
	return buf.Bytes()
}

func fillCircle(img *image.RGBA, cx, cy, r float64, col color.RGBA) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, col)
			}
		}
	}
}

// makeSpinnerFrames 以基础图标为底,生成带环绕小圆点的动画帧(ICO 字节)
func makeSpinnerFrames() [][]byte {
	src, err := png.Decode(bytes.NewReader(kimiPngBytes))
	if err != nil {
		return nil
	}
	bounds := src.Bounds()
	w := bounds.Dx()
	h := bounds.Dy()
	radius := float64(min(w, h)) * 0.36
	dot := math.Max(3, float64(min(w, h))/6)
	cx, cy := float64(w)/2, float64(h)/2
	blue := color.RGBA{R: 59, G: 130, B: 246, A: 255}

	frames := make([][]byte, 0, spinnerFrameCount)
	for i := 0; i < spinnerFrameCount; i++ {
		angle := float64(i)*2*math.Pi/spinnerFrameCount - math.Pi/2
		x := cx + radius*math.Cos(angle)
		y := cy + radius*math.Sin(angle)
		frame := image.NewRGBA(bounds)
		for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
			for px := bounds.Min.X; px < bounds.Max.X; px++ {
				frame.Set(px, py, src.At(px, py))
			}
		}
		fillCircle(frame, x, y, dot/2, blue)
		var buf bytes.Buffer
		if png.Encode(&buf, frame) != nil {
			continue
		}
		frames = append(frames, pngToIco(buf.Bytes(), w, h))
	}
	return frames
}
