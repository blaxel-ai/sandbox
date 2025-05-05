package common

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ParseResponse parses a JSON response into a provided model
func ParseResponse(resp *http.Response, model interface{}) error {
	if resp.StatusCode >= 400 {
		return fmt.Errorf("API returned error status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	err = json.Unmarshal(body, model)
	if err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	return nil
}

// MakeRequestAndParse makes an API request and parses the response into the provided model
func MakeRequestAndParse(method, path string, requestBody interface{}, responseModel interface{}) (*http.Response, error) {
	resp, err := MakeRequest(method, path, requestBody)
	if err != nil {
		return resp, fmt.Errorf("request failed: %w", err)
	}

	if responseModel != nil {
		err = ParseResponse(resp, responseModel)
		if err != nil {
			resp.Body.Close()
			return resp, err
		}
	}

	return resp, nil
}
