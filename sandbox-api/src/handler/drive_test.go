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
