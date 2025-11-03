package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Ensure MorphClient implements Client and CodeReranker interfaces
var _ Client = (*MorphClient)(nil)
var _ CodeReranker = (*MorphClient)(nil)

// MorphClient handles communication with the Morph API
type MorphClient struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

// MorphRequest represents the request structure for Morph API
type MorphRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

// MorphResponse represents the response structure from Morph API
type MorphResponse struct {
	Choices []Choice `json:"choices"`
}

// NewMorphClient creates a new Morph API client
func NewMorphClient(apiKey string) *MorphClient {
	return &MorphClient{
		APIKey:  apiKey,
		BaseURL: "https://api.morphllm.com/v1",
		Client:  &http.Client{},
	}
}

// ProviderName returns the name of the provider
func (m *MorphClient) ProviderName() string {
	return "morphllm"
}

// ApplyCodeEdit uses Morph's API to apply code edits more precisely
func (m *MorphClient) ApplyCodeEdit(originalContent, codeEdit, model string) (string, error) {
	// Default to "auto" if not specified
	if model == "" {
		model = "auto"
	}

	// Prepare the request payload
	content := fmt.Sprintf("<code>%s</code>\n<update>%s</update>", originalContent, codeEdit)

	requestBody := MorphRequest{
		Model: model,
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
	defer func() { _ = resp.Body.Close() }()

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

// MorphRerankRequest represents the request structure for Morph reranking API
type MorphRerankRequest struct {
	Model     string   `json:"model"`
	Query     string   `json:"query"`
	Documents []string `json:"documents"`
	TopN      int      `json:"top_n,omitempty"`
}

// MorphRerankResponse represents the response structure from Morph reranking API
type MorphRerankResponse struct {
	Model   string              `json:"model"`
	Results []MorphRerankResult `json:"results"`
}

// MorphRerankResult represents a single reranked result
type MorphRerankResult struct {
	Index          int                  `json:"index"`
	Document       *MorphRerankDocument `json:"document,omitempty"`
	RelevanceScore float64              `json:"relevance_score"`
}

// MorphRerankDocument represents the document object in rerank results
type MorphRerankDocument struct {
	Text string `json:"text"`
}

// RerankCode performs semantic search/reranking on code documents using Morph's rerank API
func (m *MorphClient) RerankCode(documents []CodebaseDocument, query string, tokenLimit int) ([]RankedFile, error) {
	if len(documents) == 0 {
		return []RankedFile{}, nil
	}

	// Convert documents to string array for Morph API
	documentStrings := make([]string, len(documents))
	for i, doc := range documents {
		documentStrings[i] = doc.Content
	}

	// Prepare the request payload
	requestBody := MorphRerankRequest{
		Model:     "morph-rerank-v3",
		Query:     query,
		Documents: documentStrings,
		// Note: Morph API doesn't have a direct token_limit parameter
		// The top_n parameter can be used to limit results if needed
	}

	// Marshal the request to JSON
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request to the rerank endpoint
	req, err := http.NewRequest("POST", m.BaseURL+"/rerank", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.APIKey)

	// Make the request
	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to Morph API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("morph API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Read and parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var rerankResponse MorphRerankResponse
	if err := json.Unmarshal(body, &rerankResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Convert to RankedFile format
	rankedFiles := make([]RankedFile, 0, len(rerankResponse.Results))
	for _, result := range rerankResponse.Results {
		// Use the index to get the original document path
		if result.Index >= 0 && result.Index < len(documents) {
			rankedFiles = append(rankedFiles, RankedFile{
				Path:    documents[result.Index].Path,
				Content: documents[result.Index].Content,
				Score:   result.RelevanceScore,
			})
		}
	}

	return rankedFiles, nil
}
