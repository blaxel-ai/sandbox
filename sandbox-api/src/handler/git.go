package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	gitpkg "github.com/blaxel-ai/sandbox-api/src/handler/git"
	"github.com/blaxel-ai/sandbox-api/src/lib"
)

// GitHandler handles git operations
type GitHandler struct {
	*BaseHandler
	gitManager *gitpkg.GitManager
	fsHandler  *FileSystemHandler
}

// NewGitHandler creates a new git handler
func NewGitHandler(fsHandler *FileSystemHandler) *GitHandler {
	workingDir, err := fsHandler.GetWorkingDirectory()
	if err != nil || workingDir == "" {
		workingDir = "/"
	}

	// Use the same root as filesystem (default "/")
	root := "/"

	return &GitHandler{
		BaseHandler: NewBaseHandler(),
		gitManager:  gitpkg.NewGitManager(root, workingDir),
		fsHandler:   fsHandler,
	}
}

// GitCloneRequest is the request body for cloning a repository
type GitCloneRequest struct {
	URL      string `json:"url" example:"https://github.com/user/repo.git" binding:"required"`
	Path     string `json:"path" example:"workspace/repo" binding:"required"`
	Branch   string `json:"branch" example:"main"`
	Username string `json:"username" example:"git"`
	Password string `json:"password" example:"personal_access_token"`
} // @name GitCloneRequest

// GitCloneResponse is the response body for cloning a repository
type GitCloneResponse struct {
	Path    string `json:"path" example:"workspace/repo"`
	Message string `json:"message" example:"Repository cloned successfully"`
} // @name GitCloneResponse

// GitStatusResponse is the response body for repository status
type GitStatusResponse struct {
	CurrentBranch string              `json:"currentBranch" example:"main"`
	FileStatus    []gitpkg.FileStatus `json:"fileStatus"`
	Ahead         int                 `json:"ahead" example:"2"`
	Behind        int                 `json:"behind" example:"0"`
} // @name GitStatusResponse

// GitBranchesResponse is the response body for listing branches
type GitBranchesResponse struct {
	Branches []string `json:"branches" example:"main,develop,feature/new-feature"`
} // @name GitBranchesResponse

// GitBranchRequest is the request body for branch operations
type GitBranchRequest struct {
	Name string `json:"name" example:"feature/new-feature" binding:"required"`
} // @name GitBranchRequest

// GitAddRequest is the request body for staging files
type GitAddRequest struct {
	Files []string `json:"files" example:"file1.txt,file2.txt" binding:"required"`
} // @name GitAddRequest

// GitCommitRequest is the request body for creating a commit
type GitCommitRequest struct {
	Message string `json:"message" example:"feat: add new feature" binding:"required"`
	Author  string `json:"author" example:"John Doe" binding:"required"`
	Email   string `json:"email" example:"john@example.com" binding:"required"`
} // @name GitCommitRequest

// GitCommitResponse is the response body for creating a commit
type GitCommitResponse struct {
	CommitHash string `json:"commitHash" example:"abc123def456"`
	Message    string `json:"message" example:"Commit created successfully"`
} // @name GitCommitResponse

// GitPushPullRequest is the request body for push/pull operations
type GitPushPullRequest struct {
	Username string `json:"username" example:"git"`
	Password string `json:"password" example:"personal_access_token"`
} // @name GitPushPullRequest

// extractPathFromRequest extracts the repository path from the request
func (h *GitHandler) extractPathFromRequest(c *gin.Context) string {
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
		// Special case: /git/ means current directory
		return "."
	} else if strings.HasPrefix(path, "/") {
		// Remove leading slash for relative paths like /src -> src
		return path[1:]
	}

	return path
}

// HandleClone handles POST requests to /git/clone
// @Summary Clone a git repository
// @Description Clone a git repository to the specified path
// @Tags git
// @Accept json
// @Produce json
// @Param request body GitCloneRequest true "Clone request"
// @Success 200 {object} GitCloneResponse "Repository cloned successfully"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/clone [post]
func (h *GitHandler) HandleClone(c *gin.Context) {
	var request GitCloneRequest
	if err := h.BindJSON(c, &request); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Validate URL
	if err := gitpkg.ValidateGitURL(request.URL); err != nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid URL: %w", err))
		return
	}

	// Validate path
	if err := gitpkg.ValidatePath(request.Path); err != nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid path: %w", err))
		return
	}

	// Format path
	path, err := lib.FormatPath(request.Path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Clone repository
	err = h.gitManager.Clone(request.URL, path, request.Branch, request.Username, request.Password)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to clone repository: %w", err))
		return
	}

	h.SendJSON(c, http.StatusOK, GitCloneResponse{
		Path:    path,
		Message: "Repository cloned successfully",
	})
}

// HandleStatus handles GET requests to /git/status/:path
// @Summary Get repository status
// @Description Get the status of a git repository
// @Tags git
// @Produce json
// @Param path path string true "Repository path"
// @Success 200 {object} GitStatusResponse "Repository status"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/status/{path} [get]
func (h *GitHandler) HandleStatus(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Get repository status
	status, err := h.gitManager.Status(path)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to get repository status: %w", err))
		return
	}

	h.SendJSON(c, http.StatusOK, status)
}

// HandleBranches handles GET requests to /git/branches/:path
// @Summary List branches
// @Description List all branches in a git repository
// @Tags git
// @Produce json
// @Param path path string true "Repository path"
// @Success 200 {object} GitBranchesResponse "List of branches"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/branches/{path} [get]
func (h *GitHandler) HandleBranches(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Get branches
	branches, err := h.gitManager.Branches(path)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to get branches: %w", err))
		return
	}

	h.SendJSON(c, http.StatusOK, branches)
}

// HandleCreateBranch handles POST requests to /git/branches/:path
// @Summary Create a new branch
// @Description Create a new branch in a git repository
// @Tags git
// @Accept json
// @Produce json
// @Param path path string true "Repository path"
// @Param request body GitBranchRequest true "Branch name"
// @Success 200 {object} SuccessResponse "Branch created successfully"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/branches/{path} [post]
func (h *GitHandler) HandleCreateBranch(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	var request GitBranchRequest
	if err := h.BindJSON(c, &request); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Create branch
	err = h.gitManager.CreateBranch(path, request.Name)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to create branch: %w", err))
		return
	}

	h.SendSuccessWithPath(c, path, fmt.Sprintf("Branch '%s' created successfully", request.Name))
}

// HandleCheckoutBranch handles PUT requests to /git/branch/:name/:path
// @Summary Checkout a branch
// @Description Switch to a different branch in a git repository
// @Tags git
// @Produce json
// @Param name path string true "Branch name"
// @Param path path string true "Repository path"
// @Success 200 {object} SuccessResponse "Branch checked out successfully"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/branch/{name}/{path} [put]
func (h *GitHandler) HandleCheckoutBranch(c *gin.Context) {
	path := h.extractPathFromRequest(c)
	branchName := c.Param("name")

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	if branchName == "" {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("branch name is required"))
		return
	}

	// Checkout branch
	err = h.gitManager.CheckoutBranch(path, branchName)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to checkout branch: %w", err))
		return
	}

	h.SendSuccessWithPath(c, path, fmt.Sprintf("Branch '%s' checked out successfully", branchName))
}

// HandleDeleteBranch handles DELETE requests to /git/branch/:name/:path
// @Summary Delete a branch
// @Description Delete a branch from a git repository
// @Tags git
// @Produce json
// @Param name path string true "Branch name"
// @Param path path string true "Repository path"
// @Success 200 {object} SuccessResponse "Branch deleted successfully"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/branch/{name}/{path} [delete]
func (h *GitHandler) HandleDeleteBranch(c *gin.Context) {
	path := h.extractPathFromRequest(c)
	branchName := c.Param("name")

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	if branchName == "" {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("branch name is required"))
		return
	}

	// Delete branch
	err = h.gitManager.DeleteBranch(path, branchName)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to delete branch: %w", err))
		return
	}

	h.SendSuccessWithPath(c, path, fmt.Sprintf("Branch '%s' deleted successfully", branchName))
}

// HandleAdd handles POST requests to /git/add/:path
// @Summary Stage files
// @Description Add files to the staging area
// @Tags git
// @Accept json
// @Produce json
// @Param path path string true "Repository path"
// @Param request body GitAddRequest true "Files to stage"
// @Success 200 {object} SuccessResponse "Files staged successfully"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/add/{path} [post]
func (h *GitHandler) HandleAdd(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	var request GitAddRequest
	if err := h.BindJSON(c, &request); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	if len(request.Files) == 0 {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("at least one file is required"))
		return
	}

	// Add files
	err = h.gitManager.Add(path, request.Files)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to add files: %w", err))
		return
	}

	h.SendSuccessWithPath(c, path, "Files staged successfully")
}

// HandleCommit handles POST requests to /git/commit/:path
// @Summary Create a commit
// @Description Create a commit with staged changes
// @Tags git
// @Accept json
// @Produce json
// @Param path path string true "Repository path"
// @Param request body GitCommitRequest true "Commit details"
// @Success 200 {object} GitCommitResponse "Commit created successfully"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/commit/{path} [post]
func (h *GitHandler) HandleCommit(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	var request GitCommitRequest
	if err := h.BindJSON(c, &request); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Create commit
	commitHash, err := h.gitManager.Commit(path, request.Message, request.Author, request.Email)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to create commit: %w", err))
		return
	}

	h.SendJSON(c, http.StatusOK, GitCommitResponse{
		CommitHash: commitHash,
		Message:    "Commit created successfully",
	})
}

// HandlePush handles POST requests to /git/push/:path
// @Summary Push commits
// @Description Push commits to remote repository
// @Tags git
// @Accept json
// @Produce json
// @Param path path string true "Repository path"
// @Param request body GitPushPullRequest false "Authentication credentials"
// @Success 200 {object} SuccessResponse "Commits pushed successfully"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/push/{path} [post]
func (h *GitHandler) HandlePush(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	var request GitPushPullRequest
	// Authentication is optional for push/pull
	_ = h.BindJSON(c, &request)

	// Push commits
	err = h.gitManager.Push(path, request.Username, request.Password)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to push: %w", err))
		return
	}

	h.SendSuccessWithPath(c, path, "Commits pushed successfully")
}

// HandlePull handles POST requests to /git/pull/:path
// @Summary Pull commits
// @Description Pull commits from remote repository
// @Tags git
// @Accept json
// @Produce json
// @Param path path string true "Repository path"
// @Param request body GitPushPullRequest false "Authentication credentials"
// @Success 200 {object} SuccessResponse "Commits pulled successfully"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /git/pull/{path} [post]
func (h *GitHandler) HandlePull(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	var request GitPushPullRequest
	// Authentication is optional for push/pull
	_ = h.BindJSON(c, &request)

	// Pull commits
	err = h.gitManager.Pull(path, request.Username, request.Password)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("failed to pull: %w", err))
		return
	}

	h.SendSuccessWithPath(c, path, "Commits pulled successfully")
}
