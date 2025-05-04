package lib

import (
	"fmt"
	"os"
	"strings"
)

func FormatPath(path string) (string, error) {
	// Default to root if path is empty
	if path == "" {
		path = "/"
	}
	// Ensure path starts with a slash
	if path != "/" && len(path) > 0 && path[0] != '/' {
		path = "/" + path
	}
	if strings.HasPrefix(path, "//") {
		path = path[1:]
	}
	if strings.HasPrefix(path, "/~") {
		if os.Getenv("HOME") == "" {
			return "", fmt.Errorf("home directory not found")
		}
		path = os.Getenv("HOME") + path[2:]
	}
	return path, nil
}
