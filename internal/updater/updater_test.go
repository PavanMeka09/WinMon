package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateBinary_ValidMZHeader(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "dummy_executable.exe")

	// Create a dummy binary starting with "MZ" header
	content := []byte("MZ\x90\x00\x03\x00\x00\x00\x04\x00\x00\x00\xff\xff")
	if err := os.WriteFile(tempFile, content, 0755); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	err := ValidateBinary(tempFile)
	if err != nil {
		t.Errorf("expected binary with 'MZ' header to validate successfully, got: %v", err)
	}
}

func TestValidateBinary_InvalidHeader(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "textfile.txt")

	content := []byte("This is a plain text file without MZ header")
	if err := os.WriteFile(tempFile, content, 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	err := ValidateBinary(tempFile)
	if err == nil {
		t.Error("expected error validating non-executable file, got nil")
	}
}

func TestValidateBinary_NonExistentFile(t *testing.T) {
	err := ValidateBinary(filepath.Join(t.TempDir(), "non_existent.exe"))
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}
