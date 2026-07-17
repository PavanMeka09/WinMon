package audio

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

var (
	winmm              = syscall.NewLazyDLL("winmm.dll")
	procMciSendStringW = winmm.NewProc("mciSendStringW")
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
		return err
	}
	procMciSendStringW.Call(uintptr(unsafe.Pointer(openPtr)), 0, 0, 0)

	playPtr, _ := syscall.UTF16PtrFromString("play myaudio wait")
	procMciSendStringW.Call(uintptr(unsafe.Pointer(playPtr)), 0, 0, 0)

	closePtr, _ := syscall.UTF16PtrFromString("close myaudio")
	procMciSendStringW.Call(uintptr(unsafe.Pointer(closePtr)), 0, 0, 0)

	return nil
}

// getVolumeScript returns the C# compilation script for controlling system volume.
func getVolumeScript(action string) string {
	base := `
Add-Type -TypeDefinition @"
using System.Runtime.InteropServices;
[Guid("5CDF2C82-841E-4546-9722-0CF74078229A"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
interface IAudioEndpointVolume {
    int f(); int g(); int h(); int i();
    int SetMasterVolumeLevelScalar(float fLevel, System.Guid pguidEventContext);
    int j();
    int GetMasterVolumeLevelScalar(out float pfLevel);
    int k(); int l(); int m(); int n();
    int SetMute([MarshalAs(UnmanagedType.Bool)] bool bMute, System.Guid pguidEventContext);
    int GetMute(out bool pbMute);
}
[Guid("D666063F-1587-4E43-81F1-B948E807363F"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
interface IMMDevice { int Activate(ref System.Guid id, int clsCtx, int activationParams, out IAudioEndpointVolume aev); }
[Guid("A95664D2-9614-4F35-A746-DE8DB63617E6"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
interface IMMDeviceEnumerator { int f(); int GetDefaultAudioEndpoint(int dataFlow, int role, out IMMDevice endpoint); }
[ComImport, Guid("BCDE0395-E52F-467C-8E3D-C4579291692E")] class MMDeviceEnumeratorComObject { }

public class Audio {
    static IAudioEndpointVolume Vol() {
        var enumerator = new MMDeviceEnumeratorComObject() as IMMDeviceEnumerator;
        IMMDevice dev = null;
        Marshal.ThrowExceptionForHR(enumerator.GetDefaultAudioEndpoint(0, 1, out dev));
        IAudioEndpointVolume epv = null;
        var epvid = typeof(IAudioEndpointVolume).GUID;
        Marshal.ThrowExceptionForHR(dev.Activate(ref epvid, 23, 0, out epv));
        return epv;
    }
    public static float Volume {
        get { float vol; Marshal.ThrowExceptionForHR(Vol().GetMasterVolumeLevelScalar(out vol)); return vol; }
        set { Marshal.ThrowExceptionForHR(Vol().SetMasterVolumeLevelScalar(value, System.Guid.Empty)); }
    }
    public static bool Mute {
        get { bool mute; Marshal.ThrowExceptionForHR(Vol().GetMute(out mute)); return mute; }
        set { Marshal.ThrowExceptionForHR(Vol().SetMute(value, System.Guid.Empty)); }
    }
}
"@
`
	return base + "\n" + action
}

// SetVolume sets the master system volume (0 to 100).
func SetVolume(percent int) error {
	if percent < 0 {
		percent = 0
	} else if percent > 100 {
		percent = 100
	}
	val := float32(percent) / 100.0
	action := fmt.Sprintf("[Audio]::Volume = %.2f", val)
	return runPowerShell(getVolumeScript(action))
}

// SetMute sets the master system mute state.
func SetMute(mute bool) error {
	var action string
	if mute {
		action = "[Audio]::Mute = $true"
	} else {
		action = "[Audio]::Mute = $false"
	}
	return runPowerShell(getVolumeScript(action))
}
