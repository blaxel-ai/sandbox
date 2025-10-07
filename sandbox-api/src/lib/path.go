package lib

import (
	"fmt"
	"os"
	"strings"
)

func FormatPath(path string) (string, error) {
	// Default to current directory if path is empty
	if path == "" {
		path = "."
	}

	// Handle home directory expansion
	if strings.HasPrefix(path, "~") {
		if os.Getenv("HOME") == "" {
			return "", fmt.Errorf("home directory not found")
		}
		path = os.Getenv("HOME") + path[1:]
	}

	// Clean up double slashes
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}

	return path, nil
}
