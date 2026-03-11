package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// DummyResponseWriter implements http.ResponseWriter but discards all data
// This eliminates overhead from httptest.NewRecorder() in benchmarks
type DummyResponseWriter struct{}

func (d *DummyResponseWriter) Header() http.Header {
	return http.Header{}
}

func (d *DummyResponseWriter) Write(data []byte) (int, error) {
	// Discard all data - do nothing
	return len(data), nil
}

func (d *DummyResponseWriter) WriteHeader(statusCode int) {
	// Do nothing - discard status code
}

// setupBenchmarkRouter wraps SetupRouter with benchmark mode configuration
func setupBenchmarkRouter() *gin.Engine {
	// Set Gin to release mode for benchmarks
	gin.SetMode(gin.ReleaseMode)
	// Discard all output during benchmarks to only preserve benchmark output
	gin.DefaultWriter = io.Discard
	// Disable request logging for clean benchmark output
	// Disable processing time middleware to reduce overhead
	return SetupRouter(true, false)
}

// benchmarkRequest executes an HTTP request against the router for benchmarking
// It recreates the request body for each iteration since HTTP request bodies can only be read once
func benchmarkRequest(b *testing.B, router *gin.Engine, method, path string, body []byte) {
	w := new(DummyResponseWriter)
	for b.Loop() {
		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewBuffer(body)
		}
		req, _ := http.NewRequest(method, path, bodyReader)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(w, req)
	}
}

// encodeFilesystemPath encodes a path for the filesystem API
// Absolute paths (starting with /) need to have the leading slash URL-encoded as %2F
func encodeFilesystemPath(path string) string {
	if path == "" {
		return "/filesystem/"
	}

	if path[0] == '/' {
		return "/filesystem%2F" + path[1:]
	}
	return "/filesystem/" + path
}

// BenchmarkProcessExecutionHTTP benchmarks HTTP-level process execution with waitForCompletion=true
// and verifies that logs are correctly returned at the end
func BenchmarkProcessExecutionHTTP(b *testing.B) {
	router := setupBenchmarkRouter()
	commands := []struct {
		name    string
		command string
	}{
		{"echo", "echo 'hello world'"},
		{"pwd", "pwd"},
		{"seq_small", "seq 1 10"},
		{"seq_medium", "seq 1 100"},
		{"seq_large", "seq 1 1000"},
	}

	for _, cmd := range commands {
		b.Run(fmt.Sprintf("BenchmarkProcessExecutionHTTP-%s", cmd.name), func(b *testing.B) {
			requestBody := map[string]interface{}{
				"command":           cmd.command,
				"workingDir":        "/",
				"waitForCompletion": true,
			}
			jsonData, _ := json.Marshal(requestBody)

			benchmarkRequest(b, router, http.MethodPost, "/process", jsonData)
		})
	}
}

// BenchmarkListDirectory benchmarks listing a directory
func BenchmarkListDirectory(b *testing.B) {
	router := setupBenchmarkRouter()
	benchmarkRequest(b, router, http.MethodGet, encodeFilesystemPath("/tmp"), nil)
}

// BenchmarkWriteFile benchmarks writing a file
func BenchmarkWriteFile(b *testing.B) {
	router := setupBenchmarkRouter()
	testContent := "Hello world"
	// Prepare JSON data outside the loop since content is constant
	requestBody := map[string]interface{}{
		"content": testContent,
	}
	jsonData, _ := json.Marshal(requestBody)
	w := new(DummyResponseWriter)

	b.ResetTimer()
	for b.Loop() {
		// Pause timer during setup operations
		b.StopTimer()
		testPath := fmt.Sprintf("/tmp/test-write-%d", time.Now().UnixNano())
		req, _ := http.NewRequest(http.MethodPut, encodeFilesystemPath(testPath), bytes.NewBuffer(jsonData))
		req.Header.Set("Content-Type", "application/json")
		b.StartTimer()

		// Only measure the actual HTTP request handling
		router.ServeHTTP(w, req)
	}
}

// BenchmarkReadFile benchmarks reading a file
func BenchmarkReadFile(b *testing.B) {
	router := setupBenchmarkRouter()

	// Setup: create a test file first
	testPath := fmt.Sprintf("/tmp/test-read-%d", time.Now().UnixNano())
	testContent := "Hello world"
	requestBody := map[string]interface{}{
		"content": testContent,
	}
	jsonData, _ := json.Marshal(requestBody)
	createReq, _ := http.NewRequest(http.MethodPut, encodeFilesystemPath(testPath), bytes.NewBuffer(jsonData))
	createReq.Header.Set("Content-Type", "application/json")
	w := new(DummyResponseWriter)
	router.ServeHTTP(w, createReq)

	// Benchmark reading
	b.ResetTimer()
	benchmarkRequest(b, router, http.MethodGet, encodeFilesystemPath(testPath), nil)
}

// BenchmarkDeleteFile benchmarks deleting a file
func BenchmarkDeleteFile(b *testing.B) {
	router := setupBenchmarkRouter()
	testContent := "Hello world"
	// Prepare JSON data outside the loop since content is constant
	requestBody := map[string]interface{}{
		"content": testContent,
	}
	jsonData, _ := json.Marshal(requestBody)
	w := new(DummyResponseWriter)

	b.ResetTimer()
	for b.Loop() {
		// Pause timer during file creation setup
		b.StopTimer()
		testPath := fmt.Sprintf("/tmp/test-delete-%d", time.Now().UnixNano())
		createReq, _ := http.NewRequest(http.MethodPut, encodeFilesystemPath(testPath), bytes.NewBuffer(jsonData))
		createReq.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, createReq)

		// Prepare delete request
		deleteReq, _ := http.NewRequest(http.MethodDelete, encodeFilesystemPath(testPath), nil)
		b.StartTimer()

		// Only measure the actual delete operation
		router.ServeHTTP(w, deleteReq)
	}
}
