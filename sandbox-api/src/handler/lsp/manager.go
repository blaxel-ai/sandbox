package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// LSPManager manages LSP server instances
type LSPManager struct {
	servers map[string]*LSPServer
	mu      sync.RWMutex
}

// NewLSPManager creates a new LSP manager
func NewLSPManager() *LSPManager {
	return &LSPManager{
		servers: make(map[string]*LSPServer),
	}
}

var (
	lspManagerInstance *LSPManager
	lspManagerOnce     sync.Once
)

// GetLSPManager returns the singleton LSP manager instance
func GetLSPManager() *LSPManager {
	lspManagerOnce.Do(func() {
		lspManagerInstance = NewLSPManager()
	})
	return lspManagerInstance
}

// CreateLSPServer creates and initializes a new LSP server
func (m *LSPManager) CreateLSPServer(languageID LanguageID, projectPath string) (*LSPServer, error) {
	// Validate language ID
	if languageID != LanguageIDPython && languageID != LanguageIDTypeScript && languageID != LanguageIDJavaScript {
		return nil, fmt.Errorf("unsupported language: %s", languageID)
	}

	// Resolve project path
	absProjectPath, err := m.resolveProjectPath(projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve project path: %w", err)
	}

	// Get the command for the language server
	cmd, err := m.getLanguageServerCommand(languageID)
	if err != nil {
		return nil, err
	}

	// Set working directory to project path
	cmd.Dir = absProjectPath

	// Set up pipes for stdin, stdout, and stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the language server process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start language server: %w", err)
	}

	// Create LSP server instance
	server := &LSPServer{
		ID:          uuid.New().String(),
		LanguageID:  languageID,
		ProjectPath: projectPath,
		ProcessPID:  cmd.Process.Pid,
		Status:      "initializing",
		CreatedAt:   time.Now(),
		stdin:       stdin,
		stdout:      stdout,
		stderr:      stderr,
		reqCounter:  0,
		initialized: false,
	}

	// Store server
	m.mu.Lock()
	m.servers[server.ID] = server
	m.mu.Unlock()

	// Start stderr reader to log errors
	stderrDone := make(chan bool, 1)
	go m.readStderr(server, stderrDone)

	// Give the process a moment to start
	time.Sleep(100 * time.Millisecond)

	// Check if the process is still running
	select {
	case <-stderrDone:
		server.Status = "error"
		server.ErrorMsg = "language server process exited immediately"
		return server, fmt.Errorf("language server process exited immediately, check if the language server is installed")
	default:
		// Process is still running, continue
	}

	// Initialize the LSP server
	if err := m.initializeServer(server, absProjectPath); err != nil {
		server.Status = "error"
		server.ErrorMsg = err.Error()
		// Try to cleanup
		_ = m.shutdownServer(server)
		return server, fmt.Errorf("failed to initialize LSP server: %w", err)
	}

	server.Status = "ready"
	server.initialized = true

	logrus.Infof("LSP server created: %s (%s) for project: %s", server.ID, languageID, projectPath)
	return server, nil
}

// GetLSPServer retrieves an LSP server by ID
func (m *LSPManager) GetLSPServer(id string) (*LSPServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	server, exists := m.servers[id]
	if !exists {
		return nil, fmt.Errorf("LSP server not found: %s", id)
	}

	return server, nil
}

// ListLSPServers returns all LSP servers
func (m *LSPManager) ListLSPServers() []*LSPServer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	servers := make([]*LSPServer, 0, len(m.servers))
	for _, server := range m.servers {
		servers = append(servers, server)
	}

	return servers
}

// DeleteLSPServer shuts down and removes an LSP server
func (m *LSPManager) DeleteLSPServer(id string) error {
	m.mu.Lock()
	server, exists := m.servers[id]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("LSP server not found: %s", id)
	}
	delete(m.servers, id)
	m.mu.Unlock()

	// Shutdown the server
	if err := m.shutdownServer(server); err != nil {
		logrus.Warnf("Error shutting down LSP server %s: %v", id, err)
	}

	logrus.Infof("LSP server deleted: %s", id)
	return nil
}

// GetCompletions requests code completions from an LSP server
func (m *LSPManager) GetCompletions(serverID string, filePath string, line int, character int) (*CompletionList, error) {
	server, err := m.GetLSPServer(serverID)
	if err != nil {
		return nil, err
	}

	if !server.initialized || server.Status != "ready" {
		return nil, fmt.Errorf("LSP server not ready")
	}

	// Convert file path to URI
	absFilePath, err := m.resolveFilePath(server.ProjectPath, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve file path: %w", err)
	}
	fileURI := fmt.Sprintf("file://%s", absFilePath)

	// Send textDocument/completion request
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: fileURI},
		Position:     Position{Line: line, Character: character},
	}

	result, err := m.sendRequest(server, "textDocument/completion", params)
	if err != nil {
		return nil, fmt.Errorf("completion request failed: %w", err)
	}

	// Parse the result
	completionList, err := m.parseCompletionResult(result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse completion result: %w", err)
	}

	return completionList, nil
}

// getLanguageServerCommand returns the command to start the language server
func (m *LSPManager) getLanguageServerCommand(languageID LanguageID) (*exec.Cmd, error) {
	switch languageID {
	case LanguageIDPython:
		// Check for pyright-langserver (globally installed)
		if _, err := exec.LookPath("pyright-langserver"); err == nil {
			return exec.Command("pyright-langserver", "--stdio"), nil
		}
		// Fallback to npx with pyright package (contains pyright-langserver binary)
		return exec.Command("npx", "--yes", "-p", "pyright", "pyright-langserver", "--stdio"), nil

	case LanguageIDTypeScript, LanguageIDJavaScript:
		// Check if typescript-language-server is available
		if _, err := exec.LookPath("typescript-language-server"); err == nil {
			return exec.Command("typescript-language-server", "--stdio"), nil
		}
		// Fallback to npx with --yes flag to auto-install
		return exec.Command("npx", "--yes", "typescript-language-server", "--stdio"), nil

	default:
		return nil, fmt.Errorf("unsupported language: %s", languageID)
	}
}

// resolveProjectPath resolves the project path to an absolute path
func (m *LSPManager) resolveProjectPath(projectPath string) (string, error) {
	if filepath.IsAbs(projectPath) {
		// Verify the directory exists
		if _, err := os.Stat(projectPath); err != nil {
			return "", fmt.Errorf("project path does not exist: %s", projectPath)
		}
		return projectPath, nil
	}

	// Relative path - resolve from current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	absPath := filepath.Join(cwd, projectPath)
	if _, err := os.Stat(absPath); err != nil {
		return "", fmt.Errorf("project path does not exist: %s", absPath)
	}

	return absPath, nil
}

// resolveFilePath resolves a file path relative to the project path
func (m *LSPManager) resolveFilePath(projectPath string, filePath string) (string, error) {
	if filepath.IsAbs(filePath) {
		return filePath, nil
	}

	// Resolve project path first
	absProjectPath, err := m.resolveProjectPath(projectPath)
	if err != nil {
		return "", err
	}

	return filepath.Join(absProjectPath, filePath), nil
}

// initializeServer sends the initialize request to the LSP server
func (m *LSPManager) initializeServer(server *LSPServer, rootPath string) error {
	rootURI := fmt.Sprintf("file://%s", rootPath)

	params := InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   rootURI,
		Capabilities: ClientCapabilities{
			TextDocument: &TextDocumentClientCapabilities{
				Completion: &CompletionClientCapabilities{
					CompletionItem: &CompletionItemCapabilities{
						SnippetSupport: false,
					},
				},
			},
		},
	}

	result, err := m.sendRequest(server, "initialize", params)
	if err != nil {
		return fmt.Errorf("initialize request failed: %w", err)
	}

	// Parse initialize result
	var initResult InitializeResult
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal initialize result: %w", err)
	}
	if err := json.Unmarshal(resultBytes, &initResult); err != nil {
		return fmt.Errorf("failed to parse initialize result: %w", err)
	}

	// Send initialized notification
	if err := m.sendNotification(server, "initialized", map[string]interface{}{}); err != nil {
		return fmt.Errorf("initialized notification failed: %w", err)
	}

	return nil
}

// shutdownServer sends shutdown and exit to the LSP server
func (m *LSPManager) shutdownServer(server *LSPServer) error {
	if server.stdin == nil {
		return nil
	}

	// Send shutdown request
	_, _ = m.sendRequest(server, "shutdown", nil)

	// Send exit notification
	_ = m.sendNotification(server, "exit", nil)

	// Close pipes
	_ = server.stdin.Close()
	_ = server.stdout.Close()
	_ = server.stderr.Close()

	return nil
}

// sendRequest sends a JSON-RPC request and waits for the response
func (m *LSPManager) sendRequest(server *LSPServer, method string, params interface{}) (interface{}, error) {
	server.reqCounter++
	reqID := server.reqCounter

	request := LSPRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  params,
	}

	// Marshal request
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create content-length header
	message := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(requestBytes), requestBytes)

	// Send request
	if _, err := server.stdin.Write([]byte(message)); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	}

	// Read response with timeout
	responseChan := make(chan *LSPResponse, 1)
	errorChan := make(chan error, 1)

	go func() {
		response, err := m.readResponse(server)
		if err != nil {
			errorChan <- err
			return
		}
		responseChan <- response
	}()

	// Wait for response or timeout
	var response *LSPResponse
	select {
	case response = <-responseChan:
		// Got response
	case err := <-errorChan:
		return nil, fmt.Errorf("failed to read response: %w", err)
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("timeout waiting for LSP response")
	}

	// Check for errors
	if response.Error != nil {
		return nil, fmt.Errorf("LSP error: %s (code: %d)", response.Error.Message, response.Error.Code)
	}

	return response.Result, nil
}

// sendNotification sends a JSON-RPC notification (no response expected)
func (m *LSPManager) sendNotification(server *LSPServer, method string, params interface{}) error {
	notification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}

	// Marshal notification
	notificationBytes, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("failed to marshal notification: %w", err)
	}

	// Create content-length header
	message := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(notificationBytes), notificationBytes)

	// Send notification
	if _, err := server.stdin.Write([]byte(message)); err != nil {
		return fmt.Errorf("failed to write notification: %w", err)
	}

	return nil
}

// readResponse reads a JSON-RPC response from the LSP server
func (m *LSPManager) readResponse(server *LSPServer) (*LSPResponse, error) {
	reader := bufio.NewReader(server.stdout)

	// Read headers
	var contentLength int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read header: %w", err)
		}

		line = line[:len(line)-1] // Remove \n
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1] // Remove \r
		}

		if line == "" {
			break // End of headers
		}

		if _, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength); err == nil {
			continue
		}
	}

	if contentLength == 0 {
		return nil, fmt.Errorf("no Content-Length header found")
	}

	// Read body
	body := make([]byte, contentLength)
	if _, err := reader.Read(body); err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}

	// Parse response
	var response LSPResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

// readStderr reads and logs stderr output from the LSP server
func (m *LSPManager) readStderr(server *LSPServer, done chan bool) {
	defer func() {
		done <- true
	}()
	scanner := bufio.NewScanner(server.stderr)
	for scanner.Scan() {
		logrus.Debugf("LSP server %s stderr: %s", server.ID, scanner.Text())
	}
}

// parseCompletionResult parses the completion result
func (m *LSPManager) parseCompletionResult(result interface{}) (*CompletionList, error) {
	if result == nil {
		return &CompletionList{IsIncomplete: false, Items: []CompletionItem{}}, nil
	}

	// Marshal and unmarshal to convert to CompletionList
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	// Try to parse as CompletionList first
	var completionList CompletionList
	if err := json.Unmarshal(resultBytes, &completionList); err == nil {
		return &completionList, nil
	}

	// Try to parse as array of CompletionItem
	var items []CompletionItem
	if err := json.Unmarshal(resultBytes, &items); err == nil {
		return &CompletionList{IsIncomplete: false, Items: items}, nil
	}

	return nil, fmt.Errorf("failed to parse completion result")
}
