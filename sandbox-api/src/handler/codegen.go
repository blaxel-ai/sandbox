package handler

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/blaxel-ai/sandbox-api/src/lib"
	"github.com/blaxel-ai/sandbox-api/src/lib/codegen"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// CodegenHandler handles code generation requests (fastapply and reranking)
type CodegenHandler struct {
	BaseHandler
	FileSystem *FileSystemHandler
}

// NewCodegenHandler creates a new codegen handler
func NewCodegenHandler(fsHandler *FileSystemHandler) *CodegenHandler {
	return &CodegenHandler{
		FileSystem: fsHandler,
	}
}

// extractPathFromRequest extracts the path from the request (same logic as FileSystemHandler)
func (h *CodegenHandler) extractPathFromRequest(c *gin.Context) string {
	path := c.Param("path")

	// Check if the request URL explicitly contains %2F (encoded /)
	rawURL := c.Request.URL.RawPath
	if rawURL == "" {
		rawURL = c.Request.URL.Path
	}

	// If the raw URL contains %2F, it's an explicit absolute path request
	if strings.Contains(rawURL, "%2F") {
		// Keep the path as-is for absolute paths
		return path
	}

	// If path starts with "/" but doesn't have %2F in the URL, treat as relative
	// by removing the leading slash (Gin adds it)
	if path == "/" {
		// Special case: /codegen/fastapply/ means current directory
		return "."
	} else if strings.HasPrefix(path, "/") {
		// Remove leading slash for relative paths like /src -> src
		return path[1:]
	}

	return path
}

// ApplyEditRequest represents the request body for applying code edits
type ApplyEditRequest struct {
	CodeEdit string `json:"codeEdit" binding:"required" example:"// Add world parameter\nfunction hello(world) {\n  console.log('Hello', world);\n}"`
	Model    string `json:"model,omitempty" example:"auto"`
} // @name ApplyEditRequest

// ApplyEditResponse represents the response for applying code edits
type ApplyEditResponse struct {
	Success         bool   `json:"success" example:"true"`
	Path            string `json:"path" example:"src/main.js"`
	OriginalContent string `json:"originalContent" example:"function hello() {\n  console.log('Hello');\n}"`
	UpdatedContent  string `json:"updatedContent" example:"function hello(world) {\n  console.log('Hello', world);\n}"`
	Provider        string `json:"provider" example:"Relace"`
	Message         string `json:"message,omitempty" example:"Code edit applied successfully"`
} // @name ApplyEditResponse

// HandleFastApply applies a code edit using the configured LLM provider
// @Summary Apply code edit
// @Description Uses the configured LLM provider (Relace or Morph) to apply a code edit to the original content.
// @Description
// @Description To use this endpoint as an agent tool, follow these guidelines:
// @Description
// @Description Use this tool to make an edit to an existing file. This will be read by a less intelligent model, which will quickly apply the edit. You should make it clear what the edit is, while also minimizing the unchanged code you write.
// @Description
// @Description When writing the edit, you should specify each edit in sequence, with the special comment "// ... existing code ..." to represent unchanged code in between edited lines.
// @Description
// @Description Example format:
// @Description // ... existing code ...
// @Description FIRST_EDIT
// @Description // ... existing code ...
// @Description SECOND_EDIT
// @Description // ... existing code ...
// @Description THIRD_EDIT
// @Description // ... existing code ...
// @Description
// @Description You should still bias towards repeating as few lines of the original file as possible to convey the change. But, each edit should contain minimally sufficient context of unchanged lines around the code you're editing to resolve ambiguity.
// @Description
// @Description DO NOT omit spans of pre-existing code (or comments) without using the "// ... existing code ..." comment to indicate its absence. If you omit the existing code comment, the model may inadvertently delete these lines.
// @Description
// @Description If you plan on deleting a section, you must provide context before and after to delete it. If the initial code is "Block 1\nBlock 2\nBlock 3", and you want to remove Block 2, you would output "// ... existing code ...\nBlock 1\nBlock 3\n// ... existing code ...".
// @Description
// @Description Make sure it is clear what the edit should be, and where it should be applied. Make edits to a file in a single edit_file call instead of multiple edit_file calls to the same file. The apply model can handle many distinct edits at once.
// @Tags fastapply
// @Accept json
// @Produce json
// @Param path path string true "Path to the file to edit (relative to workspace)"
// @Param request body ApplyEditRequest true "Code edit request"
// @Success 200 {object} ApplyEditResponse "Code edit applied successfully"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity - failed to process the request"
// @Failure 503 {object} ErrorResponse "Service unavailable - no provider configured"
// @Router /codegen/fastapply/{path} [put]
func (h *CodegenHandler) HandleFastApply(c *gin.Context) {
	// Check if fastapply is enabled
	if !codegen.IsEnabled() {
		h.SendError(c, http.StatusBadRequest,
			fmt.Errorf("codegen tools are not configured, follow this documentation to configure it: https://docs.blaxel.ai/Sandboxes/Codegen"))
		return
	}

	// Get the file path from the URL using the same logic as filesystem handler
	filePath := h.extractPathFromRequest(c)

	// Format the path
	filePath, err := lib.FormatPath(filePath)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	if filePath == "" {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("file path is required"))
		return
	}

	// Parse request body
	var req ApplyEditRequest
	if err := h.BindJSON(c, &req); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Check if path is a directory
	isDir, err := h.FileSystem.DirectoryExists(filePath)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to check path: %w", err))
		return
	}
	if isDir {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("path is a directory, not a file"))
		return
	}

	// Check if file exists and read its content
	fileExists, err := h.FileSystem.FileExists(filePath)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to check if file exists: %w", err))
		return
	}

	var originalContent string
	if fileExists {
		file, err := h.FileSystem.ReadFile(filePath)
		if err != nil {
			h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to read file: %w", err))
			return
		}
		originalContent = string(file.Content)
	}

	// Create client
	client, err := codegen.NewClient()
	if err != nil {
		logrus.Errorf("Failed to create fastapply client: %v", err)
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	// Apply the code edit
	model := req.Model
	if model == "" {
		model = "auto"
	}
	logrus.Infof("Applying code edit to %s using %s provider with model %s", filePath, client.ProviderName(), model)
	updatedContent, err := client.ApplyCodeEdit(originalContent, req.CodeEdit, model)
	if err != nil {
		logrus.Errorf("Failed to apply code edit: %v", err)
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	// Write the updated content back to the file
	err = h.FileSystem.WriteFile(filePath, []byte(updatedContent), 0644)
	if err != nil {
		logrus.Errorf("Failed to write file: %v", err)
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to write file: %w", err))
		return
	}

	// Return the result
	c.JSON(http.StatusOK, ApplyEditResponse{
		Success:         true,
		Path:            filePath,
		OriginalContent: originalContent,
		UpdatedContent:  updatedContent,
		Provider:        client.ProviderName(),
		Message:         fmt.Sprintf("Code edit applied successfully to %s", filePath),
	})
}

// RerankingRequest represents the query parameters for code reranking
type RerankingRequest struct {
	Query          string  `form:"query" binding:"required" example:"user authentication middleware"`
	ScoreThreshold float64 `form:"scoreThreshold" example:"0.5"`
	TokenLimit     int     `form:"tokenLimit" example:"30000"`
	FilePattern    string  `form:"filePattern" example:".*\\.ts$"`
} // @name RerankingRequest

// RerankingResponse represents the response for code reranking
type RerankingResponse struct {
	Success bool         `json:"success" example:"true"`
	Files   []RankedFile `json:"files"`
	Message string       `json:"message,omitempty" example:"Found 5 relevant files"`
} // @name RerankingResponse

// RankedFile represents a ranked file in the reranking response
type RankedFile = codegen.RankedFile // @name RankedFile

// HandleReranking performs semantic search/reranking on code files
// @Summary Code reranking/semantic search
// @Description Uses Relace's code reranking model to find the most relevant files for a given query. This is useful as a first pass in agentic exploration to narrow down the search space.
// @Description
// @Description Based on: https://docs.relace.ai/docs/code-reranker/agent
// @Description
// @Description Query Construction: The query can be a short question or a more detailed conversation with the user request included. For a first pass, use the full conversation; for subsequent calls, use more targeted questions.
// @Description
// @Description Token Limit and Score Threshold: For 200k token context models like Claude 4 Sonnet, recommended defaults are scoreThreshold=0.5 and tokenLimit=30000.
// @Description
// @Description The response will be a list of file paths and contents ordered from most relevant to least relevant.
// @Tags codegen
// @Produce json
// @Param path path string true "Path to search in (relative to workspace)"
// @Param query query string true "Natural language query to search for"
// @Param scoreThreshold query number false "Minimum relevance score (default: 0.5)"
// @Param tokenLimit query int false "Maximum tokens to return (default: 30000)"
// @Param filePattern query string false "Regex pattern to filter files (e.g., .*\\.ts$ for TypeScript files)"
// @Success 200 {object} RerankingResponse "Relevant files found"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity - failed to process the request"
// @Failure 503 {object} ErrorResponse "Service unavailable - Relace not configured"
// @Router /codegen/reranking/{path} [get]
func (h *CodegenHandler) HandleReranking(c *gin.Context) {
	// Check if codegen tools are enabled (we need Relace for reranking)
	if !codegen.IsEnabled() {
		h.SendError(c, http.StatusBadRequest,
			fmt.Errorf("codegen tools are not configured, follow this documentation to configure it: https://docs.blaxel.ai/Sandboxes/Codegen"))
		return
	}

	// Get the directory path from the URL using the same logic as filesystem handler
	directory := h.extractPathFromRequest(c)
	// Format the path
	directory, err := lib.FormatPath(directory)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	if directory == "" {
		directory = "."
	}

	// Parse query parameters
	var req RerankingRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Set defaults
	scoreThreshold := req.ScoreThreshold
	if scoreThreshold == 0 {
		scoreThreshold = 0.5
	}

	tokenLimit := req.TokenLimit
	if tokenLimit == 0 {
		tokenLimit = 30000
	}

	// Check if directory exists
	isDir, err := h.FileSystem.DirectoryExists(directory)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to check directory: %w", err))
		return
	}
	if !isDir {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("path is not a directory"))
		return
	}

	// Create client
	client, err := codegen.NewClient()
	if err != nil {
		logrus.Errorf("Failed to create fastapply client: %v", err)
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	// Check if the client supports reranking (only Relace does)
	reranker, ok := client.(codegen.CodeReranker)
	if !ok {
		h.SendError(c, http.StatusServiceUnavailable,
			fmt.Errorf("code reranking is only available with Relace. Set RELACE_API_KEY to use this feature"))
		return
	}

	// Collect documents from the directory
	documents, err := h.collectDocumentsFromDirectory(directory, req.FilePattern)
	if err != nil {
		logrus.Errorf("Failed to collect documents: %v", err)
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	if len(documents) == 0 {
		c.JSON(http.StatusOK, RerankingResponse{
			Success: true,
			Files:   []RankedFile{},
			Message: "No files found matching criteria",
		})
		return
	}

	// Perform reranking
	logrus.Infof("Performing code reranking on %d files using %s", len(documents), client.ProviderName())
	rankedFiles, err := reranker.RerankCode(documents, req.Query, tokenLimit)
	if err != nil {
		logrus.Errorf("Failed to rerank code: %v", err)
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	// Filter by score threshold
	filteredFiles := make([]RankedFile, 0)
	for _, file := range rankedFiles {
		if file.Score >= scoreThreshold {
			filteredFiles = append(filteredFiles, file)
		}
	}

	// Return the result
	c.JSON(http.StatusOK, RerankingResponse{
		Success: true,
		Files:   filteredFiles,
		Message: fmt.Sprintf("Found %d relevant files", len(filteredFiles)),
	})
}

// collectDocumentsFromDirectory walks a directory and collects all eligible code files
func (h *CodegenHandler) collectDocumentsFromDirectory(directory, filePattern string) ([]codegen.CodebaseDocument, error) {
	// Compile regex pattern if provided
	var fileRegex *regexp.Regexp
	if filePattern != "" {
		var err error
		fileRegex, err = regexp.Compile(filePattern)
		if err != nil {
			return nil, fmt.Errorf("invalid file pattern regex: %w", err)
		}
	}

	documents := []codegen.CodebaseDocument{}

	err := filepath.WalkDir(directory, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		fileSplittered := strings.Split(path, "/")
		filename := fileSplittered[len(fileSplittered)-1]
		// Skip hidden files and common directories to ignore
		if h.shouldSkipFile(path) {
			return nil
		}

		// Apply file pattern filter if provided
		if fileRegex != nil && !fileRegex.MatchString(path) && !fileRegex.MatchString(filename) {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			// Skip files we can't read
			return nil
		}

		// Skip binary files
		if h.isBinaryFile(content) {
			return nil
		}

		// Add to documents
		documents = append(documents, codegen.CodebaseDocument{
			Path:    path,
			Content: string(content),
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	return documents, nil
}

// shouldSkipFile determines if a file should be skipped during directory traversal
func (h *CodegenHandler) shouldSkipFile(path string) bool {
	// Skip hidden files and directories
	base := filepath.Base(path)
	if len(base) > 0 && base[0] == '.' {
		return true
	}

	// Skip common directories to ignore
	skipDirs := []string{"node_modules", "vendor", ".git", "dist", "build", "target", "__pycache__", ".venv"}
	for _, dir := range skipDirs {
		if filepath.Base(filepath.Dir(path)) == dir {
			return true
		}
	}

	return false
}

// isBinaryFile checks if a file is binary by looking for null bytes
// and checking the ratio of non-printable characters
func (h *CodegenHandler) isBinaryFile(content []byte) bool {
	return false
}
