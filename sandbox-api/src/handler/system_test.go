package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestResolveUpgradeVersion verifies the default upgrade version logic.
// Regression test for ENG-2974: an empty version must resolve to "latest"
// (the most recent published release) rather than "develop", which could
// silently downgrade the running binary.
func TestResolveUpgradeVersion(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		want      string
	}{
		{name: "empty defaults to latest", requested: "", want: "latest"},
		{name: "explicit develop is preserved", requested: "develop", want: "develop"},
		{name: "explicit main is preserved", requested: "main", want: "main"},
		{name: "explicit tag is preserved", requested: "v1.0.0", want: "v1.0.0"},
		{name: "explicit latest is preserved", requested: "latest", want: "latest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveUpgradeVersion(tt.requested); got != tt.want {
				t.Errorf("resolveUpgradeVersion(%q) = %q, want %q", tt.requested, got, tt.want)
			}
		})
	}
}

func TestUpgradeRejectsUntrustedBinarySources(t *testing.T) {
	for _, body := range []string{
		`{"baseUrl":"http://127.0.0.1:8000/releases"}`,
		`{"baseUrl":"https://github.com/another-owner/sandbox/releases"}`,
		`{"version":"../other-repo"}`,
		`{"version":"v1.0.0%2Fother"}`,
		`{"version":"https://internal.example"}`,
	} {
		t.Run(body, func(t *testing.T) {
			writer := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(writer)
			context.Request = httptest.NewRequest(http.MethodPost, "/upgrade", strings.NewReader(body))
			context.Request.Header.Set("Content-Type", "application/json")
			(&SystemHandler{BaseHandler: NewBaseHandler()}).HandleUpgrade(context)
			if writer.Code != http.StatusBadRequest {
				t.Fatalf("expected rejection before saving state, got %d: %s", writer.Code, writer.Body.String())
			}
		})
	}
}

func TestValidUpgradeVersion(t *testing.T) {
	for _, version := range []string{"latest", "main", "develop", "v1.0.0", "v0.2.21-beta.1"} {
		if !validUpgradeVersion(version) {
			t.Errorf("expected %q to be allowed", version)
		}
	}
	for _, version := range []string{"", "../v1.0.0", "v1.0.0/other", "v1.0.0%2fother", "v1.0.0?x=y", "v1.0.0#part", "https://example.com"} {
		if validUpgradeVersion(version) {
			t.Errorf("expected %q to be rejected", version)
		}
	}
}
