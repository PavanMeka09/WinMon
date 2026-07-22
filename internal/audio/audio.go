package audio

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winmm              = syscall.NewLazyDLL("winmm.dll")
	procMciSendStringW = winmm.NewProc("mciSendStringW")

	ole32                = syscall.NewLazyDLL("ole32.dll")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
)

var (
	CLSID_MMDeviceEnumerator = windows.GUID{
		Data1: 0xBCDE0395,
		Data2: 0xE52F,
		Data3: 0x467C,
		Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E},
	}
	IID_IMMDeviceEnumerator = windows.GUID{
		Data1: 0xA95664D2,
		Data2: 0x9614,
		Data3: 0x4F35,
		Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6},
	}
	IID_IAudioEndpointVolume = windows.GUID{
		Data1: 0x5CDF2C82,
		Data2: 0x841E,
		Data3: 0x4546,
		Data4: [8]byte{0x97, 0x22, 0x0C, 0xF7, 0x40, 0x78, 0x22, 0x9A},
	}
)

func runPowerShell(cmd string) error {
	c := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmd)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return c.Run()
}

// SpeakTTS speaks the given text using the Windows Speech API via PowerShell.
func SpeakTTS(text string) error {
	// Escape single quotes for PowerShell
	escapedText := strings.ReplaceAll(text, "'", "''")
	cmd := fmt.Sprintf(`Add-Type -AssemblyName System.Speech; $synth = New-Object System.Speech.Synthesis.SpeechSynthesizer; $synth.Speak('%s')`, escapedText)
	return runPowerShell(cmd)
}

// PlaySoundLocal plays an audio file (WAV/MP3) using Windows MCI.
func PlaySoundLocal(path string) error {
	openCmd := fmt.Sprintf("open \"%s\" type mpegvideo alias myaudio", path)
	openPtr, err := syscall.UTF16PtrFromString(openCmd)
	if err != nil {
		return fmt.Errorf("invalid open command string: %w", err)
	}
	procMciSendStringW.Call(uintptr(unsafe.Pointer(openPtr)), 0, 0, 0)

	playPtr, err := syscall.UTF16PtrFromString("play myaudio wait")
	if err != nil {
		return fmt.Errorf("invalid play command string: %w", err)
	}
	procMciSendStringW.Call(uintptr(unsafe.Pointer(playPtr)), 0, 0, 0)

	closePtr, err := syscall.UTF16PtrFromString("close myaudio")
	if err != nil {
		return fmt.Errorf("invalid close command string: %w", err)
	}
	procMciSendStringW.Call(uintptr(unsafe.Pointer(closePtr)), 0, 0, 0)

	return nil
}

func setVolumeCOM(volume float32, mute *bool) error {
	// COINIT_APARTMENTTHREADED = 2
	procCoInitializeEx.Call(0, 2)
	defer procCoUninitialize.Call()

	var enumerator unsafe.Pointer
	ret, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)),
		0,
		23, // CLSCTX_ALL
		uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enumerator)),
	)
	if ret != 0 {
		return fmt.Errorf("CoCreateInstance failed: HRESULT 0x%X", ret)
	}
	defer releaseCOM(enumerator)

	var device unsafe.Pointer
	// GetDefaultAudioEndpoint (offset 4)
	vtable := *(*unsafe.Pointer)(enumerator)
	fn := *(*uintptr)(unsafe.Add(vtable, 4*unsafe.Sizeof(uintptr(0))))
	ret, _, _ = syscall.SyscallN(fn, uintptr(enumerator), 0, 0, uintptr(unsafe.Pointer(&device)))
	if ret != 0 {
		return fmt.Errorf("GetDefaultAudioEndpoint failed: HRESULT 0x%X", ret)
	}
	defer releaseCOM(device)

	var endpointVolume unsafe.Pointer
	// Activate (offset 3)
	vtable = *(*unsafe.Pointer)(device)
	fn = *(*uintptr)(unsafe.Add(vtable, 3*unsafe.Sizeof(uintptr(0))))
	ret, _, _ = syscall.SyscallN(fn, uintptr(device), uintptr(unsafe.Pointer(&IID_IAudioEndpointVolume)), 23, 0, uintptr(unsafe.Pointer(&endpointVolume)))
	if ret != 0 {
		return fmt.Errorf("Activate failed: HRESULT 0x%X", ret)
	}
	defer releaseCOM(endpointVolume)

	vtable = *(*unsafe.Pointer)(endpointVolume)
	if mute != nil {
		// SetMute (offset 14)
		var val uintptr = 0
		if *mute {
			val = 1
		}
		fn = *(*uintptr)(unsafe.Add(vtable, 14*unsafe.Sizeof(uintptr(0))))
		ret, _, _ = syscall.SyscallN(fn, uintptr(endpointVolume), val, 0)
		if ret != 0 {
			return fmt.Errorf("SetMute failed: HRESULT 0x%X", ret)
		}
	} else {
		// SetMasterVolumeLevelScalar (offset 7)
		fn = *(*uintptr)(unsafe.Add(vtable, 7*unsafe.Sizeof(uintptr(0))))
		ret = syscallVolume(fn, uintptr(endpointVolume), volume, 0)
		if ret != 0 {
			return fmt.Errorf("SetMasterVolumeLevelScalar failed: HRESULT 0x%X", ret)
		}
	}

	return nil
}

func releaseCOM(obj unsafe.Pointer) {
	if obj == nil {
		return
	}
	vtable := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtable, 2*unsafe.Sizeof(uintptr(0)))) // Release is offset 2
	syscall.SyscallN(fn, uintptr(obj))
}

// SetVolume sets the master system volume (0 to 100).
func SetVolume(percent int) error {
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	val := float32(percent) / 100.0
	return setVolumeCOM(val, nil)
}

// SetMute sets the master system mute state.
func SetMute(mute bool) error {
	return setVolumeCOM(0.0, &mute)
}
