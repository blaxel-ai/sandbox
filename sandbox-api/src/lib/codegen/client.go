package codegen

import (
	"fmt"
	"os"
)

// Client is an interface for code editing LLM clients
type Client interface {
	ApplyCodeEdit(originalContent, codeEdit, model string) (string, error)
	ProviderName() string
}

// CodeReranker is an interface for semantic code search/reranking
type CodeReranker interface {
	Client
	RerankCode(documents []CodebaseDocument, query string, tokenLimit int) ([]RankedFile, error)
}

// CodebaseDocument represents a document to be ranked
type CodebaseDocument struct {
	Path    string
	Content string
}

// RankedFile represents a file with its relevance score
type RankedFile struct {
	Path    string  `json:"path"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// Provider represents different LLM providers
type Provider string

const (
	ProviderMorph  Provider = "morph"
	ProviderRelace Provider = "relace"
)

// IsEnabled checks if any fastapply provider is configured
func IsEnabled() bool {
	return os.Getenv("RELACE_API_KEY") != "" || os.Getenv("MORPH_API_KEY") != ""
}

// NewClient creates a new code editing client based on environment variables
// It checks for RELACE_API_KEY first, then falls back to MORPH_API_KEY
func NewClient() (Client, error) {
	// Check for Relace first
	if apiKey := os.Getenv("RELACE_API_KEY"); apiKey != "" {
		return NewRelaceClient(apiKey), nil
	}

	// Fall back to Morph
	if apiKey := os.Getenv("MORPH_API_KEY"); apiKey != "" {
		return NewMorphClient(apiKey), nil
	}

	return nil, fmt.Errorf("no API key found: set either RELACE_API_KEY or MORPH_API_KEY")
}

// Message represents a chat message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Choice represents a choice in the response
type Choice struct {
	Message Message `json:"message"`
}
