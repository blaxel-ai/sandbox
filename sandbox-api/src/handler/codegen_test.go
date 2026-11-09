package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blaxel-ai/sandbox-api/src/lib/codegen"
	"github.com/gin-gonic/gin"
)

func TestHandleRerankingUnavailable(t *testing.T) {
	for _, env := range []string{"RELACE_API_KEY", "MORPH_API_KEY"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv("RELACE_API_KEY", "")
			t.Setenv("MORPH_API_KEY", "")
			t.Setenv(env, "key")

			h := NewCodegenHandler(&FileSystemHandler{BaseHandler: NewBaseHandler()})
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodGet, "/codegen/reranking/?query=auth", nil)
			c.Params = gin.Params{{Key: "path", Value: "/" + t.TempDir()}}
			c.Request.URL.RawPath = "/codegen/reranking/%2F"

			h.HandleReranking(c)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503: %s", response.Code, response.Body.String())
			}
			var result ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Error != codegen.ErrRerankingUnavailable.Error() {
				t.Fatalf("error = %q", result.Error)
			}
		})
	}
}
