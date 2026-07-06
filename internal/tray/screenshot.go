package tray

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"syscall"
	"unsafe"
)

// Win32 procs for screenshot capture.
var (
	pGetWindowRect  = user32.NewProc("GetWindowRect")
	pGetWindowDC    = user32.NewProc("GetWindowDC")
	pReleaseDC      = user32.NewProc("ReleaseDC")
	pPrintWindow    = user32.NewProc("PrintWindow")
	pCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject   = gdi32.NewProc("SelectObject")
	pBitBlt         = gdi32.NewProc("BitBlt")
	pDeleteDC       = gdi32.NewProc("DeleteDC")
	pDeleteObject   = gdi32.NewProc("DeleteObject")
	pGetDIBits      = gdi32.NewProc("GetDIBits")

	gdi32 = syscall.NewLazyDLL("gdi32.dll")
)

const (
	pwRenderfullContent = 0x00000002
	dibRgbColors        = 0
	biRgb               = 0
	capblist            = 0x40 // RC_BITBLT
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type rect struct {
	Left, Top, Right, Bottom int32
}

// CaptureWindow captures the content of a Win32 window handle to a PNG file.
// Returns the path to the saved PNG, or an error.
func CaptureWindow(hwnd uintptr, outPath string) error {
	if hwnd == 0 {
		return fmt.Errorf("invalid window handle")
	}

	// Get window dimensions.
	var r rect
	_, _, _ = pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	width := int(r.Right - r.Left)
	height := int(r.Bottom - r.Top)
	if width <= 0 || height <= 0 {
		return fmt.Errorf("invalid window size: %dx%d", width, height)
	}

	// Get device context.
	hdc, _, _ := pGetWindowDC.Call(hwnd)
	if hdc == 0 {
		return fmt.Errorf("GetWindowDC failed")
	}
	defer pReleaseDC.Call(hwnd, hdc)

	// Create compatible DC and bitmap.
	hdcMem, _, _ := pCreateCompatibleDC.Call(hdc)
	if hdcMem == 0 {
		return fmt.Errorf("CreateCompatibleDC failed")
	}
	defer pDeleteDC.Call(hdcMem)

	hBitmap, _, _ := pCreateCompatibleBitmap.Call(hdc, uintptr(width), uintptr(height))
	if hBitmap == 0 {
		return fmt.Errorf("CreateCompatibleBitmap failed")
	}
	defer pDeleteObject.Call(hBitmap)

	oldBitmap, _, _ := pSelectObject.Call(hdcMem, hBitmap)
	defer pSelectObject.Call(hdcMem, oldBitmap)

	// PrintWindow captures the window content (works even if partially occluded).
	ret, _, _ := pPrintWindow.Call(hwnd, hdcMem, pwRenderfullContent)
	if ret == 0 {
		// Fallback to BitBlt if PrintWindow fails.
		_, _, _ = pBitBlt.Call(hdcMem, 0, 0, uintptr(width), uintptr(height),
			hdc, 0, 0, 0x00CC0020) // SRCCOPY
	}

	// Read bitmap bits into Go image.
	bmi := bitmapInfoHeader{
		Size:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:       int32(width),
		Height:      int32(-height), // negative = top-down
		Planes:      1,
		BitCount:    32,
		Compression: biRgb,
	}

	pixels := make([]byte, width*height*4)
	ret2, _, _ := pGetDIBits.Call(
		hdcMem, hBitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&bmi)),
		dibRgbColors,
	)
	if ret2 == 0 {
		return fmt.Errorf("GetDIBits failed")
	}

	// Convert BGRA pixels to RGBA image.
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			idx := (y*width + x) * 4
			img.Pix[idx+0] = pixels[idx+2] // R from B
			img.Pix[idx+1] = pixels[idx+1] // G
			img.Pix[idx+2] = pixels[idx+0] // B from R
			img.Pix[idx+3] = 255           // A
		}
	}

	// Save as PNG.
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("cannot create file: %w", err)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		return fmt.Errorf("PNG encode failed: %w", err)
	}

	return nil
}
