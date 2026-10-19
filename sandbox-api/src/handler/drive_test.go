package handler

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/blaxel-ai/sandbox-api/src/handler/drive"
)

func TestMountFailureStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "filer refused the drive",
			err:  fmt.Errorf("failed to mount drive acl-and-4e2e3989: %w", drive.ErrDriveAccessDenied),
			want: http.StatusForbidden,
		},
		{
			name: "mount path taken",
			err:  fmt.Errorf("%w: /mnt/and", drive.ErrMountPathBusy),
			want: http.StatusConflict,
		},
		{
			name: "sandbox disk full",
			err:  fmt.Errorf("failed to mount drive drive-1: %w", drive.ErrInsufficientStorage),
			want: http.StatusInsufficientStorage,
		},
		{
			name: "unknown failure stays a server fault",
			err:  fmt.Errorf("failed to mount drive: blfs mount process exited unexpectedly: exit status 2"),
			want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mountFailureStatus(tt.err); got != tt.want {
				t.Fatalf("mountFailureStatus(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestUnmountFailureStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "nothing mounted at path",
			err:  fmt.Errorf("%w: /mnt/never is not a mount point", drive.ErrNotMounted),
			want: http.StatusNotFound,
		},
		{
			name: "unmount itself failed",
			err:  fmt.Errorf("failed to unmount drive: umount failed: exit status 32"),
			want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unmountFailureStatus(tt.err); got != tt.want {
				t.Fatalf("unmountFailureStatus(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
