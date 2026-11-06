package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestProcessingTimeWriterSupportsWriteDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(processingTimeMiddleware())
	r.GET("/", func(c *gin.Context) {
		if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Status(http.StatusNoContent)
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Server-Timing") == "" {
		t.Error("missing Server-Timing header")
	}
}
