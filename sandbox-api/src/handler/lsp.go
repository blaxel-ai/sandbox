package handler

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/handler/lsp"
	"github.com/blaxel-ai/sandbox-api/src/lib"
)

var (
	lspHandlerInstance *LSPHandler
	lspHandlerOnce     sync.Once
)

// GetLSPHandler returns the singleton LSP handler instance
func GetLSPHandler() *LSPHandler {
	lspHandlerOnce.Do(func() {
		lspHandlerInstance = NewLSPHandler()
	})
	return lspHandlerInstance
}

// LSPHandler handles LSP operations
type LSPHandler struct {
	*BaseHandler
	lspManager *lsp.LSPManager
}

// NewLSPHandler creates a new LSP handler
func NewLSPHandler() *LSPHandler {
	return &LSPHandler{
		BaseHandler: NewBaseHandler(),
		lspManager:  lsp.GetLSPManager(),
	}
}

// CreateLSPServerRequest is the request body for creating an LSP server
type CreateLSPServerRequest struct {
	LanguageID  string `json:"languageId" example:"python" binding:"required" enums:"python,typescript,javascript"`
	ProjectPath string `json:"projectPath" example:"/workspace/project" binding:"required"`
} // @name CreateLSPServerRequest

// LSPServerResponse is the response body for an LSP server
type LSPServerResponse struct {
	ID          string `json:"id" example:"550e8400-e29b-41d4-a716-446655440000" binding:"required"`
	LanguageID  string `json:"languageId" example:"python" binding:"required"`
	ProjectPath string `json:"projectPath" example:"/workspace/project" binding:"required"`
	ProcessPID  int    `json:"processPid" example:"12345" binding:"required"`
	Status      string `json:"status" example:"ready" binding:"required"`
	CreatedAt   string `json:"createdAt" example:"Wed, 01 Jan 2023 12:00:00 GMT" binding:"required"`
	ErrorMsg    string `json:"errorMsg,omitempty" example:""`
} // @name LSPServerResponse

// CompletionRequest is the request body for getting code completions
type CompletionRequest struct {
	FilePath  string `json:"filePath" example:"main.py" binding:"required"`
	Line      int    `json:"line" example:"10" binding:"required"`
	Character int    `json:"character" example:"15" binding:"required"`
} // @name CompletionRequest

// CompletionItemResponse represents a single completion item
type CompletionItemResponse struct {
	Label         string `json:"label" example:"print" binding:"required"`
	Kind          int    `json:"kind,omitempty" example:"3"`
	Detail        string `json:"detail,omitempty" example:"function"`
	Documentation string `json:"documentation,omitempty" example:"Print to stdout"`
	InsertText    string `json:"insertText,omitempty" example:"print()"`
	SortText      string `json:"sortText,omitempty"`
	FilterText    string `json:"filterText,omitempty"`
} // @name CompletionItemResponse

// CompletionResponse is the response body for completions
type CompletionResponse struct {
	IsIncomplete bool                     `json:"isIncomplete" example:"false" binding:"required"`
	Items        []CompletionItemResponse `json:"items" binding:"required"`
} // @name CompletionResponse

// HandleCreateLSPServer handles POST requests to /lsp
// @Summary Create an LSP server
// @Description Create a new LSP server for a specific language and project. Supported languages: python, typescript, javascript
// @Tags lsp
// @Accept json
// @Produce json
// @Param request body CreateLSPServerRequest true "LSP server creation request"
// @Success 200 {object} LSPServerResponse "LSP server information"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /lsp [post]
func (h *LSPHandler) HandleCreateLSPServer(c *gin.Context) {
	var req CreateLSPServerRequest
	if err := h.BindJSON(c, &req); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Validate and convert language ID
	languageID := lsp.LanguageID(req.LanguageID)
	if languageID != lsp.LanguageIDPython && languageID != lsp.LanguageIDTypeScript && languageID != lsp.LanguageIDJavaScript {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("unsupported language: %s (supported: python, typescript, javascript)", req.LanguageID))
		return
	}

	// Format project path
	projectPath := req.ProjectPath
	if projectPath != "" {
		formattedPath, err := lib.FormatPath(projectPath)
		if err != nil {
			h.SendError(c, http.StatusBadRequest, err)
			return
		}
		projectPath = formattedPath
	}

	// Create LSP server
	server, err := h.lspManager.CreateLSPServer(languageID, projectPath)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	response := LSPServerResponse{
		ID:          server.ID,
		LanguageID:  string(server.LanguageID),
		ProjectPath: server.ProjectPath,
		ProcessPID:  server.ProcessPID,
		Status:      server.Status,
		CreatedAt:   server.CreatedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		ErrorMsg:    server.ErrorMsg,
	}

	h.SendJSON(c, http.StatusOK, response)
}

// HandleGetLSPServer handles GET requests to /lsp/:id
// @Summary Get LSP server information
// @Description Get information about a specific LSP server
// @Tags lsp
// @Accept json
// @Produce json
// @Param id path string true "LSP server ID"
// @Success 200 {object} LSPServerResponse "LSP server information"
// @Failure 404 {object} ErrorResponse "LSP server not found"
// @Router /lsp/{id} [get]
func (h *LSPHandler) HandleGetLSPServer(c *gin.Context) {
	id, err := h.GetPathParam(c, "id")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	server, err := h.lspManager.GetLSPServer(id)
	if err != nil {
		h.SendError(c, http.StatusNotFound, err)
		return
	}

	response := LSPServerResponse{
		ID:          server.ID,
		LanguageID:  string(server.LanguageID),
		ProjectPath: server.ProjectPath,
		ProcessPID:  server.ProcessPID,
		Status:      server.Status,
		CreatedAt:   server.CreatedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		ErrorMsg:    server.ErrorMsg,
	}

	h.SendJSON(c, http.StatusOK, response)
}

// HandleListLSPServers handles GET requests to /lsp
// @Summary List all LSP servers
// @Description Get a list of all active LSP servers
// @Tags lsp
// @Accept json
// @Produce json
// @Success 200 {array} LSPServerResponse "List of LSP servers"
// @Router /lsp [get]
func (h *LSPHandler) HandleListLSPServers(c *gin.Context) {
	servers := h.lspManager.ListLSPServers()

	responses := make([]LSPServerResponse, 0, len(servers))
	for _, server := range servers {
		responses = append(responses, LSPServerResponse{
			ID:          server.ID,
			LanguageID:  string(server.LanguageID),
			ProjectPath: server.ProjectPath,
			ProcessPID:  server.ProcessPID,
			Status:      server.Status,
			CreatedAt:   server.CreatedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
			ErrorMsg:    server.ErrorMsg,
		})
	}

	h.SendJSON(c, http.StatusOK, responses)
}

// HandleDeleteLSPServer handles DELETE requests to /lsp/:id
// @Summary Delete an LSP server
// @Description Shutdown and remove an LSP server
// @Tags lsp
// @Accept json
// @Produce json
// @Param id path string true "LSP server ID"
// @Success 200 {object} SuccessResponse "LSP server deleted"
// @Failure 404 {object} ErrorResponse "LSP server not found"
// @Router /lsp/{id} [delete]
func (h *LSPHandler) HandleDeleteLSPServer(c *gin.Context) {
	id, err := h.GetPathParam(c, "id")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	err = h.lspManager.DeleteLSPServer(id)
	if err != nil {
		h.SendError(c, http.StatusNotFound, err)
		return
	}

	h.SendJSON(c, http.StatusOK, gin.H{"message": "LSP server deleted successfully"})
}

// HandleCompletions handles POST requests to /lsp/:id/completions
// @Summary Get code completions
// @Description Get code completion suggestions from an LSP server
// @Tags lsp
// @Accept json
// @Produce json
// @Param id path string true "LSP server ID"
// @Param request body CompletionRequest true "Completion request"
// @Success 200 {object} CompletionResponse "Completion items"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 404 {object} ErrorResponse "LSP server not found"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Router /lsp/{id}/completions [post]
func (h *LSPHandler) HandleCompletions(c *gin.Context) {
	id, err := h.GetPathParam(c, "id")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	var req CompletionRequest
	if err := h.BindJSON(c, &req); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Get completions from LSP server
	completionList, err := h.lspManager.GetCompletions(id, req.FilePath, req.Line, req.Character)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	// Convert to response format
	items := make([]CompletionItemResponse, 0, len(completionList.Items))
	for _, item := range completionList.Items {
		items = append(items, CompletionItemResponse{
			Label:         item.Label,
			Kind:          item.Kind,
			Detail:        item.Detail,
			Documentation: item.Documentation,
			InsertText:    item.InsertText,
			SortText:      item.SortText,
			FilterText:    item.FilterText,
		})
	}

	response := CompletionResponse{
		IsIncomplete: completionList.IsIncomplete,
		Items:        items,
	}

	h.SendJSON(c, http.StatusOK, response)
}
