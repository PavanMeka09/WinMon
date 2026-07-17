package files

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

	err = filepath.Walk(absSource, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the output zip file itself if it is being saved in the same directory
		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		absZip, _ := filepath.Abs(zipPath)
		if absPath == absZip {
			return nil
		}

		// Get relative path for the zip headers
		relPath, err := filepath.Rel(absSource, path)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
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
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})

	return err
}

// PrepareUploadPath resolves the destination path for file uploads.
// If dest is a directory, it appends the original filename.
// It also creates any missing directories along the path.
func PrepareUploadPath(dest, filename string) (string, error) {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return "", fmt.Errorf("empty destination path")
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
		finalPath = filepath.Join(dest, filename)
	} else {
		finalPath = dest
	}

	// Create any missing parent directories
	parentDir := filepath.Dir(finalPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory structure: %v", err)
	}

	return finalPath, nil
}
