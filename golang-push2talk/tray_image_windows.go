//go:build windows && (amd64 || arm64)

package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image/png"
	"unsafe"
)

//go:embed st-icon.png
var trayImage []byte

func createTrayImageIcon() (uintptr, error) {
	source, err := png.Decode(bytes.NewReader(trayImage))
	if err != nil {
		return 0, fmt.Errorf("decode tray PNG: %w", err)
	}
	size, _, _ := user32.NewProc("GetSystemMetrics").Call(49) // SM_CXSMICON
	if size < 16 || size > 256 {
		size = 32
	}
	n := int(size)
	header := struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		ClrUsed, ClrImportant  uint32
	}{Size: 40, Width: int32(n), Height: -int32(n), Planes: 1, BitCount: 32}
	var bits unsafe.Pointer
	bitmap, _, callErr := gdi32.NewProc("CreateDIBSection").Call(0, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 {
		return 0, fmt.Errorf("create tray color bitmap: %w", callErr)
	}
	defer deleteObject.Call(bitmap)
	pixels := unsafe.Slice((*byte)(bits), n*n*4)
	bounds := source.Bounds()
	// Average premultiplied pixels to preserve transparent edges at tray size.
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			x0, x1 := x*bounds.Dx()/n, max(x*bounds.Dx()/n+1, (x+1)*bounds.Dx()/n)
			y0, y1 := y*bounds.Dy()/n, max(y*bounds.Dy()/n+1, (y+1)*bounds.Dy()/n)
			var r, g, b, a uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, pa := source.At(bounds.Min.X+sx, bounds.Min.Y+sy).RGBA()
					r += uint64(pr)
					g += uint64(pg)
					b += uint64(pb)
					a += uint64(pa)
				}
			}
			count := uint64((x1-x0)*(y1-y0)) * 257
			i := (y*n + x) * 4
			pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = byte(b/count), byte(g/count), byte(r/count), byte(a/count)
		}
	}
	maskBytes := make([]byte, ((n+15)/16)*2*n)
	mask, _, callErr := gdi32.NewProc("CreateBitmap").Call(size, size, 1, 1, uintptr(unsafe.Pointer(&maskBytes[0])))
	if mask == 0 {
		return 0, fmt.Errorf("create tray mask: %w", callErr)
	}
	defer deleteObject.Call(mask)
	info := struct {
		Icon               int32
		XHotspot, YHotspot uint32
		Mask, Color        uintptr
	}{Icon: 1, Mask: mask, Color: bitmap}
	icon, _, callErr := user32.NewProc("CreateIconIndirect").Call(uintptr(unsafe.Pointer(&info)))
	if icon == 0 {
		return 0, fmt.Errorf("create tray icon: %w", callErr)
	}
	return icon, nil
}
