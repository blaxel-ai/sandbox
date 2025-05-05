package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	// Client is the HTTP client used for all requests
	Client *http.Client

	// BaseURL is the base URL for the API
	BaseURL string
)

// Initialize sets up the common package
func Initialize(baseURL string) {
	BaseURL = baseURL
	Client = &http.Client{
		Timeout: 10 * time.Second,
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
