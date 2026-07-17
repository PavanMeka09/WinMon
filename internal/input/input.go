package input

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	procSetCursorPos = user32.NewProc("SetCursorPos")
	procMouseEvent   = user32.NewProc("mouse_event")
	procSendInput    = user32.NewProc("SendInput")
	procKeybdEvent   = user32.NewProc("keybd_event")
)

const (
	MOUSEEVENTF_LEFTDOWN  = 0x0002
	MOUSEEVENTF_LEFTUP    = 0x0004
	MOUSEEVENTF_RIGHTDOWN = 0x0008
	MOUSEEVENTF_RIGHTUP   = 0x0010
	MOUSEEVENTF_WHEEL     = 0x0800

	INPUT_KEYBOARD    = 1
	KEYEVENTF_KEYUP   = 0x0002
	KEYEVENTF_UNICODE = 0x0004
)

// INPUT structure for SendInput (64-bit alignment compatible)
type INPUT struct {
	inputType uint32
	_         uint32 // alignment padding
	wVk       uint16
	wScan     uint16
	dwFlags   uint32
	time      uint32
	extraInfo uintptr
	padding   [8]byte // Union padding to match MOUSEINPUT size
}

// MoveMouse moves the cursor.
func MoveMouse(x, y int) error {
	ret, _, err := procSetCursorPos.Call(uintptr(x), uintptr(y))
	if ret == 0 {
		return err
	}
	return nil
}

// ClickMouse simulates a left click.
func ClickMouse() {
	procMouseEvent.Call(MOUSEEVENTF_LEFTDOWN|MOUSEEVENTF_LEFTUP, 0, 0, 0, 0)
}

// RightClickMouse simulates a right click.
func RightClickMouse() {
	procMouseEvent.Call(MOUSEEVENTF_RIGHTDOWN|MOUSEEVENTF_RIGHTUP, 0, 0, 0, 0)
}

// DoubleClickMouse simulates a double click.
func DoubleClickMouse() {
	ClickMouse()
	time.Sleep(100 * time.Millisecond)
	ClickMouse()
}

// ScrollMouse scrolls the mouse wheel.
func ScrollMouse(amount int) {
	procMouseEvent.Call(MOUSEEVENTF_WHEEL, 0, 0, uintptr(amount), 0)
}

// TypeText simulates typing of arbitrary Unicode text.
func TypeText(text string) {
	u16 := utf16.Encode([]rune(text))
	var inputs []INPUT

	for _, code := range u16 {
		// Key down
		inputs = append(inputs, INPUT{
			inputType: INPUT_KEYBOARD,
			wScan:     code,
			dwFlags:   KEYEVENTF_UNICODE,
		})
		// Key up
		inputs = append(inputs, INPUT{
			inputType: INPUT_KEYBOARD,
			wScan:     code,
			dwFlags:   KEYEVENTF_UNICODE | KEYEVENTF_KEYUP,
		})
	}

	if len(inputs) > 0 {
		procSendInput.Call(
			uintptr(len(inputs)),
			uintptr(unsafe.Pointer(&inputs[0])),
			unsafe.Sizeof(inputs[0]),
		)
	}
}

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

// PressKey simulates pressing a single virtual key.
func PressKey(key string) error {
	vk, err := getVKCode(key)
	if err != nil {
		return err
	}
	procKeybdEvent.Call(uintptr(vk), 0, 0, 0)
	procKeybdEvent.Call(uintptr(vk), 0, KEYEVENTF_KEYUP, 0)
	return nil
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
