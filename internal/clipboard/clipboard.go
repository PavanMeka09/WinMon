package clipboard

import (
	"github.com/atotto/clipboard"
)

// GetClipboardLocal reads text from the clipboard. This must be executed in the user's interactive session.
func GetClipboardLocal() (string, error) {
	return clipboard.ReadAll()
}

// SetClipboardLocal writes text to the clipboard. This must be executed in the user's interactive session.
func SetClipboardLocal(text string) error {
	return clipboard.WriteAll(text)
}
