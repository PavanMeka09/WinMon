package files

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"winmon/internal/service"
)

// ZipDirectory compresses a directory into a temporary ZIP file.
func ZipDirectory(sourceDir, zipPath string) error {
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	archive := zip.NewWriter(zipFile)
	defer archive.Close()

	// Convert source path to absolute path
	absSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return err
	}

	err = filepath.Walk(absSource, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			// Skip unreadable files or directories
			return nil
		}

		// Skip the output zip file itself if it is being saved in the same directory
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil
		}
		absZip, _ := filepath.Abs(zipPath)
		if absPath == absZip {
			return nil
		}

		// Get relative path for the zip headers
		relPath, err := filepath.Rel(absSource, path)
		if err != nil {
			return nil
		}

		if relPath == "." {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return nil
		}

		// Standardize separators to forward slashes for ZIP format compatibility
		header.Name = filepath.ToSlash(relPath)
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			// Skip locked or restricted files
			return nil
		}
		defer file.Close()

		_, copyErr := io.Copy(writer, file)
		if copyErr != nil {
			return copyErr
		}
		return nil
	})

	return err
}

// defaultUploadDir returns a safe absolute directory for uploads when no
// destination is specified. Using process CWD is unsafe under a Windows
// service (often C:\Windows\System32).
func defaultUploadDir() string {
	return filepath.Join(service.GetSharedTempDir(), "uploads")
}

// PrepareUploadPath resolves the destination path for file uploads safely.
// If dest is empty, it defaults to %SystemRoot%\Temp\winmon_uploads.
// If dest is a directory, it appends the sanitized filename (preventing path traversal).
// It also creates any missing parent directories along the path.
func PrepareUploadPath(dest, filename string) (string, error) {
	cleanFilename := filepath.Base(filepath.Clean(filename))
	if cleanFilename == "." || cleanFilename == "/" || cleanFilename == "\\" || cleanFilename == "" {
		cleanFilename = "uploaded_file"
	}

	dest = strings.TrimSpace(dest)
	if dest == "" {
		// Trailing separator forces directory handling even before the folder exists.
		dest = defaultUploadDir() + string(os.PathSeparator)
	}

	var finalPath string
	isDir := false

	// Check if destination ends with a path separator, or matches an existing directory
	if strings.HasSuffix(dest, "\\") || strings.HasSuffix(dest, "/") {
		isDir = true
	} else {
		info, err := os.Stat(dest)
		if err == nil && info.IsDir() {
			isDir = true
		}
	}

	if isDir {
		finalPath = filepath.Join(dest, cleanFilename)
	} else {
		finalPath = dest
	}

	finalPath = filepath.Clean(finalPath)

	// Create any missing parent directories
	parentDir := filepath.Dir(finalPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory structure: %v", err)
	}

	return finalPath, nil
}
