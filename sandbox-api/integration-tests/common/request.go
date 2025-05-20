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
	fmt.Println("Waiting for API to be ready...")

	for i := 0; i < maxRetries; i++ {
		resp, err := Client.Get(BaseURL + "/health")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			fmt.Println("API is ready!")
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(retryDelay)
		fmt.Printf("Waiting for API to be ready... (%d/%d)\n", i+1, maxRetries)
	}

	return fmt.Errorf("API did not become ready in time")
}
