package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// MorphClient handles communication with the Morph API
type MorphClient struct {
	APIKey  string
	BaseURL string
	Model   string
	Client  *http.Client
}

// MorphRequest represents the request structure for Morph API
type MorphRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MorphResponse represents the response structure from Morph API
type MorphResponse struct {
	Choices []Choice `json:"choices"`
}

// Choice represents a choice in the response
type Choice struct {
	Message Message `json:"message"`
}

// NewMorphClient creates a new Morph API client
func NewMorphClient(apiKey string) *MorphClient {
	// Get model from environment variable, default to "morph-v2"
	model := os.Getenv("MORPH_MODEL")
	if model == "" {
		model = "morph-v2"
	}

	return &MorphClient{
		APIKey:  apiKey,
		BaseURL: "https://api.morphllm.com/v1",
		Model:   model,
		Client:  &http.Client{},
	}
}

// ApplyCodeEdit uses Morph's API to apply code edits more precisely
func (m *MorphClient) ApplyCodeEdit(originalContent, codeEdit string) (string, error) {
	// Prepare the request payload
	content := fmt.Sprintf("<code>%s</code>\n<update>%s</update>", originalContent, codeEdit)

	requestBody := MorphRequest{
		Model: m.Model,
		Messages: []Message{
			{
				Role:    "user",
				Content: content,
			},
		},
		Stream: false,
	}

	// Marshal the request to JSON
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", m.BaseURL+"/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.APIKey)

	// Make the request
	resp, err := m.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request to Morph API: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("morph API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Read and parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var morphResponse MorphResponse
	if err := json.Unmarshal(body, &morphResponse); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Extract the updated content
	if len(morphResponse.Choices) == 0 {
		return "", fmt.Errorf("no choices returned from Morph API")
	}

	updatedContent := morphResponse.Choices[0].Message.Content
	if updatedContent == "" {
		return "", fmt.Errorf("empty content returned from Morph API")
	}

	return updatedContent, nil
}
