package lsp

import (
	"io"
	"time"
)

// LanguageID represents the supported programming languages for LSP
type LanguageID string

const (
	LanguageIDPython     LanguageID = "python"
	LanguageIDTypeScript LanguageID = "typescript"
	LanguageIDJavaScript LanguageID = "javascript"
)

// LSPServer represents an active LSP server instance
type LSPServer struct {
	ID          string     `json:"id"`
	LanguageID  LanguageID `json:"languageId"`
	ProjectPath string     `json:"projectPath"`
	ProcessPID  int        `json:"processPid"`
	Status      string     `json:"status"` // "initializing", "ready", "error", "shutdown"
	CreatedAt   time.Time  `json:"createdAt"`
	ErrorMsg    string     `json:"errorMsg,omitempty"`

	// Internal fields for communication
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	reqCounter  int
	initialized bool
}

// Position represents a position in a text document
type Position struct {
	Line      int `json:"line"`      // Line position in a document (zero-based)
	Character int `json:"character"` // Character offset on a line in a document (zero-based)
}

// TextDocumentIdentifier identifies a text document
type TextDocumentIdentifier struct {
	URI string `json:"uri"` // The text document's URI
}

// TextDocumentPositionParams is a parameter literal used in requests to pass a text document and a position inside that document
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// CompletionItem represents a completion item
type CompletionItem struct {
	Label         string `json:"label"`
	Kind          int    `json:"kind,omitempty"`
	Detail        string `json:"detail,omitempty"`
	Documentation string `json:"documentation,omitempty"`
	InsertText    string `json:"insertText,omitempty"`
	SortText      string `json:"sortText,omitempty"`
	FilterText    string `json:"filterText,omitempty"`
}

// CompletionList represents a collection of completion items
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

// LSPRequest represents a generic JSON-RPC request to the LSP server
type LSPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// LSPResponse represents a generic JSON-RPC response from the LSP server
type LSPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *LSPError   `json:"error,omitempty"`
}

// LSPError represents an error in an LSP response
type LSPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

// InitializeParams represents the parameters for the initialize request
type InitializeParams struct {
	ProcessID int                `json:"processId"`
	RootURI   string             `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

// ClientCapabilities represents the capabilities of the client
type ClientCapabilities struct {
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
}

// TextDocumentClientCapabilities represents text document specific client capabilities
type TextDocumentClientCapabilities struct {
	Completion *CompletionClientCapabilities `json:"completion,omitempty"`
}

// CompletionClientCapabilities represents completion specific client capabilities
type CompletionClientCapabilities struct {
	CompletionItem *CompletionItemCapabilities `json:"completionItem,omitempty"`
}

// CompletionItemCapabilities represents the capabilities of a completion item
type CompletionItemCapabilities struct {
	SnippetSupport bool `json:"snippetSupport,omitempty"`
}

// InitializeResult represents the result of an initialize request
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerCapabilities represents the capabilities of the LSP server
type ServerCapabilities struct {
	CompletionProvider interface{} `json:"completionProvider,omitempty"`
}

