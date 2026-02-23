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

func TestValidateMountPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  bool
	}{
		{"allowed /mnt", "/mnt", false},
		{"allowed under /mnt", "/mnt/data", false},
		{"allowed nested", "/mnt/foo/bar", false},
		{"forbidden /etc", "/etc", true},
		{"forbidden /root", "/root", true},
		{"forbidden /var/run/secrets", "/var/run/secrets", true},
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
