package input

import (
	"fmt"
	"strings"
	"syscall"
	"time"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	procKeybdEvent = user32.NewProc("keybd_event")
)

const KEYEVENTF_KEYUP = 0x0002

// Key Mapping for VK codes
var keyMap = map[string]byte{
	"ctrl":      0x11, // VK_CONTROL
	"alt":       0x12, // VK_MENU
	"shift":     0x10, // VK_SHIFT
	"win":       0x5B, // VK_LWIN
	"enter":     0x0D, // VK_RETURN
	"space":     0x20, // VK_SPACE
	"backspace": 0x08, // VK_BACK
	"tab":       0x09, // VK_TAB
	"esc":       0x1B, // VK_ESCAPE
	"up":        0x26, // VK_UP
	"down":      0x28, // VK_DOWN
	"left":      0x25, // VK_LEFT
	"right":     0x27, // VK_RIGHT
	"pgup":      0x21, // VK_PRIOR
	"pgdn":      0x22, // VK_NEXT
	"delete":    0x2E, // VK_DELETE
	"capslock":  0x14, // VK_CAPITAL
}

func getVKCode(key string) (byte, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	if code, ok := keyMap[key]; ok {
		return code, nil
	}
	if len(key) == 1 {
		char := key[0]
		if char >= 'a' && char <= 'z' {
			return char - 'a' + 'A', nil
		}
		if char >= '0' && char <= '9' {
			return char, nil
		}
	}
	return 0, fmt.Errorf("unknown key: %s", key)
}

// TriggerHotkey simulates pressing a modifier-combination of keys (e.g. "ctrl+c").
func TriggerHotkey(hotkey string) error {
	parts := strings.Split(hotkey, "+")
	var vks []byte

	for _, part := range parts {
		vk, err := getVKCode(part)
		if err != nil {
			return err
		}
		vks = append(vks, vk)
	}

	// Press in order
	for _, vk := range vks {
		procKeybdEvent.Call(uintptr(vk), 0, 0, 0)
	}

	time.Sleep(50 * time.Millisecond)

	// Release in reverse order
	for i := len(vks) - 1; i >= 0; i-- {
		procKeybdEvent.Call(uintptr(vks[i]), 0, KEYEVENTF_KEYUP, 0)
	}

	return nil
}
