package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareUploadPath_Directory(t *testing.T) {
	tempDir := t.TempDir()

	// PrepareUploadPath with directory target
	finalPath, err := PrepareUploadPath(tempDir, "test.txt")
	if err != nil {
		t.Fatalf("PrepareUploadPath failed: %v", err)
	}

	expected := filepath.Join(tempDir, "test.txt")
	if finalPath != expected {
		t.Errorf("expected '%s', got '%s'", expected, finalPath)
	}
}

func TestPrepareUploadPath_ExplicitFile(t *testing.T) {
	tempDir := t.TempDir()
	targetFile := filepath.Join(tempDir, "subfolder", "custom_name.txt")

	finalPath, err := PrepareUploadPath(targetFile, "ignored.txt")
	if err != nil {
		t.Fatalf("PrepareUploadPath failed: %v", err)
	}

	if finalPath != targetFile {
		t.Errorf("expected '%s', got '%s'", targetFile, finalPath)
	}

	// Verify missing parent directory structure was created
	parentDir := filepath.Dir(targetFile)
	if info, err := os.Stat(parentDir); err != nil || !info.IsDir() {
		t.Errorf("expected parent dir '%s' to exist as a directory", parentDir)
	}
}

func TestPrepareUploadPath_EmptyPath(t *testing.T) {
	_, err := PrepareUploadPath("  ", "file.txt")
	if err == nil {
		t.Error("expected error for empty destination path, got nil")
	}
}

func TestZipDirectory(t *testing.T) {
	sourceDir := t.TempDir()

	// Create dummy files inside sourceDir
	file1 := filepath.Join(sourceDir, "file1.txt")
	file2 := filepath.Join(sourceDir, "sub", "file2.txt")

	_ = os.MkdirAll(filepath.Dir(file2), 0755)
	_ = os.WriteFile(file1, []byte("hello world"), 0644)
	_ = os.WriteFile(file2, []byte("nested file content"), 0644)

	zipOutput := filepath.Join(t.TempDir(), "output.zip")

	err := ZipDirectory(sourceDir, zipOutput)
	if err != nil {
		t.Fatalf("ZipDirectory failed: %v", err)
	}

	info, err := os.Stat(zipOutput)
	if err != nil {
		t.Fatalf("failed to stat generated zip file: %v", err)
	}
	if info.Size() == 0 {
		t.Error("generated zip file is 0 bytes")
	}
}
