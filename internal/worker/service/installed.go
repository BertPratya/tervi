package service

import (
	"os"
	"path/filepath"
	"strings"
)

// IsTemporaryProgram reports whether programPath is in a temporary folder or
// any go-build folder. tempDir is normally os.TempDir and is injectable for
// tests. Both paths are resolved through symlinks before comparison.
func IsTemporaryProgram(programPath, tempDir string) bool {
	if programPath == "" {
		return false
	}
	programPath, err := resolvedAbsolutePath(programPath)
	if err != nil {
		programPath, err = filepath.Abs(programPath)
		if err != nil {
			return false
		}
	}
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	tempDir, err = resolvedAbsolutePath(tempDir)
	if err != nil {
		tempDir, err = filepath.Abs(tempDir)
		if err != nil {
			tempDir = filepath.Clean(tempDir)
		}
	}
	if pathIsWithin(tempDir, programPath) {
		return true
	}
	for _, folder := range strings.Split(programPath, string(os.PathSeparator)) {
		if folder == "go-build" {
			return true
		}
	}
	return false
}

func pathIsWithin(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}
