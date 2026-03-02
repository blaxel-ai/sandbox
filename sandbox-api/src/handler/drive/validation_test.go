package drive

import (
	"testing"
)

func TestValidateDriveName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  bool
	}{
		{"valid simple", "mydrive", false},
		{"valid with hyphen", "my-drive", false},
		{"valid with underscore", "my_drive", false},
		{"valid alphanumeric", "drive123", false},
		{"empty", "", true},
		{"slash injection", "my/drive", true},
		{"path traversal", "../etc", true},
		{"space", "my drive", true},
		{"leading hyphen", "-drive", true},
		{"invalid chars", "drive@name", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDriveName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateDriveName(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestNormalizeMountPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"/", "/"},
		{"/mnt/data", "/mnt/data"},
		{"mnt/data", "/mnt/data"},
		{"foo", "/foo"},
	}
	for _, tt := range tests {
		got := NormalizeMountPath(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeMountPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestValidateMountPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  bool
	}{
		{"absolute path", "/mnt/data", false},
		{"root", "/", false},
		{"nested", "/foo/bar/baz", false},
		{"path traversal", "/mnt/../etc", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMountPath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMountPath(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}
