package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

var (
	// Client is the HTTP client used for all requests
	Client *http.Client

	// BaseURL is the base URL for the API
	BaseURL string
)

func init() {
	BaseURL = GetEnv("API_BASE_URL", "http://localhost:8080")
	Client = &http.Client{
		Timeout: 10 * time.Second,
	}
	err := WaitForAPI(30, 1*time.Second)
	if err != nil {
		os.Exit(1)
	}
}

// MakeRequest is a helper function to make HTTP requests to the API
func MakeRequest(method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader

	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("error marshaling JSON: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, BaseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return Client.Do(req)
}

// MakeMultipartRequest is a helper function to make multipart form data requests with file uploads
func MakeMultipartRequest(method, path string, fileContent []byte, filename string, formValues map[string]string) (*http.Response, error) {
	body := &bytes.Buffer{}
	writer := NewMultipartWriter(body)

	// Add the file as a form field
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("error creating form file: %w", err)
	}
	_, err = part.Write(fileContent)
	if err != nil {
		return nil, fmt.Errorf("error writing file content: %w", err)
	}

	// Add other form values
	for key, value := range formValues {
		err = writer.WriteField(key, value)
		if err != nil {
			return nil, fmt.Errorf("error writing form field: %w", err)
		}
	}

	err = writer.Close()
	if err != nil {
		return nil, fmt.Errorf("error closing multipart writer: %w", err)
	}

	req, err := http.NewRequest(method, BaseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	return Client.Do(req)
}

// MakeMultipartRequestStream streams a multipart request body using io.Pipe
func MakeMultipartRequestStream(method, path string, fileReader io.Reader, filename string, formValues map[string]string) (*http.Response, error) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	// Write multipart content in a goroutine
	go func() {
		defer pw.Close()
		defer writer.Close()

		for key, value := range formValues {
			_ = writer.WriteField(key, value)
		}

		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, fileReader); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	req, err := http.NewRequest(method, BaseURL+path, pr)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return Client.Do(req)
}

// NewMultipartWriter creates a new multipart writer
func NewMultipartWriter(body *bytes.Buffer) *MultipartWriter {
	return &MultipartWriter{
		writer: multipart.NewWriter(body),
	}
}

// MultipartWriter wraps the multipart writer to provide a simpler interface
type MultipartWriter struct {
	writer *multipart.Writer
}

// CreateFormFile creates a new form file field
func (w *MultipartWriter) CreateFormFile(fieldname, filename string) (io.Writer, error) {
	return w.writer.CreateFormFile(fieldname, filename)
}

// WriteField writes a string field
func (w *MultipartWriter) WriteField(fieldname, value string) error {
	return w.writer.WriteField(fieldname, value)
}

// Close closes the multipart writer
func (w *MultipartWriter) Close() error {
	return w.writer.Close()
}

// FormDataContentType returns the content type with boundary
func (w *MultipartWriter) FormDataContentType() string {
	return w.writer.FormDataContentType()
}

// ParseJSONResponse parses a JSON response into the provided target
func ParseJSONResponse(resp *http.Response, target interface{}) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading response body: %w", err)
	}

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("error parsing JSON response: %w", err)
	}

	return nil
}

// WaitForAPI waits for the API to be ready by polling the health endpoint
func WaitForAPI(maxRetries int, retryDelay time.Duration) error {
	logrus.Info("Waiting for API to be ready...")

	for i := 0; i < maxRetries; i++ {
		resp, err := Client.Get(BaseURL + "/health")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			logrus.Info("API is ready!")
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(retryDelay)
		logrus.Debugf("Waiting for API to be ready... (%d/%d)", i+1, maxRetries)
	}

	return fmt.Errorf("API did not become ready in time")
}

// EncodeFilesystemPath encodes a path for the filesystem API
// Absolute paths (starting with /) need to have the leading slash URL-encoded as %2F
// Relative paths are used as-is
func EncodeFilesystemPath(path string) string {
	if path == "" {
		return "/filesystem/"
	}

	if path[0] == '/' {
		// For absolute paths, encode only the leading slash to indicate absolute path
		// The rest of the path remains as-is
		return "/filesystem%2F" + path[1:]
	}
	// For relative paths, just append to /filesystem/
	return "/filesystem/" + path
}

// EncodeWatchPath encodes a path for the watch filesystem API
// Similar to EncodeFilesystemPath but for /watch/filesystem endpoint
func EncodeWatchPath(path string) string {
	if path == "" {
		return "/watch/filesystem/"
	}

	if path[0] == '/' {
		// For absolute paths, encode only the leading slash to indicate absolute path
		return "/watch/filesystem%2F" + path[1:]
	}
	// For relative paths, just append to /watch/filesystem/
	return "/watch/filesystem/" + path
}

// EncodeTreePath encodes a path for the tree filesystem API
// Similar to EncodeFilesystemPath but for /filesystem/tree endpoint
func EncodeTreePath(path string) string {
	if path == "" {
		return "/filesystem/tree/"
	}

	if path[0] == '/' {
		// For absolute paths, encode only the leading slash to indicate absolute path
		return "/filesystem/tree%2F" + path[1:]
	}
	// For relative paths, just append to /filesystem/tree/
	return "/filesystem/tree/" + path
}
