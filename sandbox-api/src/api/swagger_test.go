package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSandboxHost(t *testing.T) {
	cases := []struct{ env, workspace, workspaceID, want string }{
		{"prod", "acme", "WS1", "sbx-box-ws1.us-pdx-1.bl.run"},
		{"prod", "baseten-q84x4yw", "WS1", "sbx-box-ws1.us-pdx-1.b10.co"},
		{"dev", "acme", "WS1", "sbx-box-ws1.us-pdx-1.runv2.blaxel.dev"},
		{"dev", "baseten-q84x4yw", "WS1", "sbx-box-ws1.us-pdx-1.dev.b10.co"},
		{"", "acme", "WS1", ""},
		{"prod", "acme", "", ""},
		// Without the workspace slug we cannot tell b10.co from bl.run.
		{"prod", "", "WS1", ""},
	}
	for _, c := range cases {
		if got := sandboxHost(c.env, c.workspace, c.workspaceID, "box", "us-pdx-1"); got != c.want {
			t.Errorf("sandboxHost(%q, %q, %q) = %q, want %q", c.env, c.workspace, c.workspaceID, got, c.want)
		}
	}
}

// doc.json must follow the environment at request time, not at startup.
func TestSwaggerDocFollowsEnvironment(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/swagger/*any", swaggerHandler())
	host := func() (string, string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/swagger/doc.json", nil))
		var doc struct{ Host, BasePath string }
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatalf("doc.json is not JSON: %v", err)
		}
		return doc.Host, doc.BasePath
	}

	t.Setenv("BL_ENV", "prod")
	t.Setenv("BL_WORKSPACE", "baseten-q84x4yw")
	t.Setenv("BL_WORKSPACE_ID", "WS1")
	t.Setenv("BL_NAME", "box")
	t.Setenv("BL_REGION", "us-pdx-1")
	if h, base := host(); h != "sbx-box-ws1.us-pdx-1.b10.co" || base != "/" {
		t.Fatalf("got host %q basePath %q", h, base)
	}

	t.Setenv("BL_NAME", "fork")
	if h, _ := host(); h != "sbx-fork-ws1.us-pdx-1.b10.co" {
		t.Fatalf("host did not follow env change: %q", h)
	}
}
