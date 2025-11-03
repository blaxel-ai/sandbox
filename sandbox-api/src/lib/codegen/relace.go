package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Ensure RelaceClient implements Client and CodeReranker interfaces
var _ Client = (*RelaceClient)(nil)
var _ CodeReranker = (*RelaceClient)(nil)

// RelaceClient handles communication with the Relace API
type RelaceClient struct {
	APIKey string
	Client *http.Client
}

// RelaceApplyRequest represents the request structure for Relace Instant Apply API
type RelaceApplyRequest struct {
	InitialCode string `json:"initial_code"`
	EditSnippet string `json:"edit_snippet"`
	Model       string `json:"model"`
	Instruction string `json:"instruction,omitempty"`
	Stream      bool   `json:"stream"`
}

// RelaceApplyResponse represents the response structure from Relace Instant Apply API
type RelaceApplyResponse struct {
	MergedCode string           `json:"mergedCode"`
	Usage      RelaceApplyUsage `json:"usage"`
}

// RelaceApplyUsage represents token usage information
type RelaceApplyUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// NewRelaceClient creates a new Relace API client
func NewRelaceClient(apiKey string) *RelaceClient {
	return &RelaceClient{
		APIKey: apiKey,
		Client: &http.Client{},
	}
}

// ProviderName returns the name of the provider
func (r *RelaceClient) ProviderName() string {
	return "relace"
}

// ApplyCodeEdit uses Relace's Instant Apply API to apply code edits
func (r *RelaceClient) ApplyCodeEdit(originalContent, codeEdit, model string) (string, error) {
	// Default to "auto" if not specified
	if model == "" {
		model = "auto"
	}

	// Prepare the request payload according to Relace API spec
	requestBody := RelaceApplyRequest{
		InitialCode: originalContent,
		EditSnippet: codeEdit,
		Model:       model,
		Stream:      false,
	}

	// Marshal the request to JSON
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request to the instant apply endpoint
	req, err := http.NewRequest("POST", "https://instantapply.endpoint.relace.run/v1/code/apply", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.APIKey)

	// Make the request
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make request to Relace API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("relace API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Read and parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	var relaceResponse RelaceApplyResponse
	if err := json.Unmarshal(body, &relaceResponse); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Extract the merged code
	if relaceResponse.MergedCode == "" {
		return "", fmt.Errorf("empty merged code returned from Relace API")
	}

	return relaceResponse.MergedCode, nil
}

// RerankRequest represents the request structure for Relace reranking API
type RerankRequest struct {
	Query      string         `json:"query"`
	Codebase   []CodebaseFile `json:"codebase"`
	TokenLimit int            `json:"token_limit"`
}

// CodebaseFile represents a file in the codebase for reranking
type CodebaseFile struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

// RerankResponse represents the response structure from Relace reranking API
type RerankResponse struct {
	Results []RerankResult `json:"results"`
}

// RerankResult represents a single result from reranking
type RerankResult struct {
	Filename string  `json:"filename"`
	Score    float64 `json:"score"`
}

// RerankCode performs semantic search/reranking on code documents
func (r *RelaceClient) RerankCode(documents []CodebaseDocument, query string, tokenLimit int) ([]RankedFile, error) {
	if len(documents) == 0 {
		return []RankedFile{}, nil
	}

	// Convert to Relace's codebase format
	codebaseFiles := make([]CodebaseFile, 0, len(documents))
	fileContents := make(map[string]string) // Store content by filename for later retrieval

	for _, doc := range documents {
		codebaseFiles = append(codebaseFiles, CodebaseFile{
			Filename: doc.Path,
			Content:  doc.Content,
		})
		fileContents[doc.Path] = doc.Content
	}

	// Prepare the request payload
	requestBody := RerankRequest{
		Query:      query,
		Codebase:   codebaseFiles,
		TokenLimit: tokenLimit,
	}

	// Marshal the request to JSON
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request to the ranker endpoint
	req, err := http.NewRequest("POST", "https://ranker.endpoint.relace.run/v2/code/rank", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.APIKey)

	// Make the request
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to Relace API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("relace API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Read and parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var rerankResponse RerankResponse
	if err := json.Unmarshal(body, &rerankResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Convert to RankedFile format
	rankedFiles := make([]RankedFile, 0, len(rerankResponse.Results))
	for _, result := range rerankResponse.Results {
		rankedFiles = append(rankedFiles, RankedFile{
			Path:    result.Filename,
			Content: fileContents[result.Filename],
			Score:   result.Score,
		})
	}

	return rankedFiles, nil
}
