package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/blaxel-ai/sandbox-api/src/lib"
)

// FileSystemHandler handles filesystem operations
type FileSystemHandler struct {
	*BaseHandler
	fs *filesystem.Filesystem
}

// FileRequest represents the request body for creating or updating a file
type FileRequest struct {
	Content     string `json:"content" example:"file contents here"`
	IsDirectory bool   `json:"isDirectory" example:"false"`
	Permissions string `json:"permissions" example:"0644"`
} // @name FileRequest

// NewFileSystemHandler creates a new filesystem handler
func NewFileSystemHandler() *FileSystemHandler {
	return &FileSystemHandler{
		BaseHandler: NewBaseHandler(),
		fs:          filesystem.NewFilesystem("/"),
	}
}

// GetWorkingDirectory gets the current working directory
func (h *FileSystemHandler) GetWorkingDirectory() (string, error) {
	return h.fs.GetAbsolutePath("/")
}

// ListDirectory lists the contents of a directory
func (h *FileSystemHandler) ListDirectory(path string) (*filesystem.Directory, error) {
	return h.fs.ListDirectory(path)
}

// ReadFile reads the contents of a file
func (h *FileSystemHandler) ReadFile(path string) (*filesystem.FileWithContentByte, error) {
	return h.fs.ReadFile(path)
}

// CreateDirectory creates a directory
func (h *FileSystemHandler) CreateDirectory(path string, permissions os.FileMode) error {
	return h.fs.CreateDirectory(path, permissions)
}

// WriteFile writes content to a file
func (h *FileSystemHandler) WriteFile(path string, content []byte, permissions os.FileMode) error {
	return h.fs.WriteFile(path, content, permissions)
}

// DirectoryExists checks if a path is a directory
func (h *FileSystemHandler) DirectoryExists(path string) (bool, error) {
	return h.fs.DirectoryExists(path)
}

// DeleteDirectory deletes a directory
func (h *FileSystemHandler) DeleteDirectory(path string, recursive bool) error {
	return h.fs.DeleteDirectory(path, recursive)
}

// FileExists checks if a path is a file
func (h *FileSystemHandler) FileExists(path string) (bool, error) {
	return h.fs.FileExists(path)
}

// DeleteFile deletes a file
func (h *FileSystemHandler) DeleteFile(path string) error {
	return h.fs.DeleteFile(path)
}

// HandleFileSystemRequest handles GET requests to /filesystem/:path
// It returns either file content or directory listing depending on the path
// @Summary Get file or directory information
// @Description Get content of a file or listing of a directory
// @Tags filesystem
// @Accept json
// @Produce json
// @Param path path string true "File or directory path"
// @Success 200 {object} filesystem.FileWithContent "File content"
// @Success 200 {object} filesystem.Directory "Directory listing"
// @Failure 404 {object} ErrorResponse "File or directory not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/{path} [get]
func (h *FileSystemHandler) HandleGetFile(c *gin.Context) {
	path, err := h.GetPathParam(c, "path")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}
	path, err = lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Check if path is a directory
	isDir, err := h.DirectoryExists(path)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	if isDir {
		h.handleListDirectory(c, path)
		return
	}

	// Check if path is a file
	isFile, err := h.FileExists(path)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	if isFile {
		h.handleReadFile(c, path)
		return
	}

	h.SendError(c, http.StatusNotFound, fmt.Errorf("file or directory not found"))
}

// handleReadFile handles requests to read a file
func (h *FileSystemHandler) handleReadFile(c *gin.Context, path string) {
	file, err := h.ReadFile(path)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error reading file: %w", err))
		return
	}

	h.SendJSON(c, http.StatusOK, gin.H{
		"path":         file.Path,
		"content":      string(file.Content),
		"permissions":  file.Permissions.String(),
		"size":         file.Size,
		"lastModified": file.LastModified,
		"owner":        file.Owner,
		"group":        file.Group,
	})
}

// handleListDirectory handles requests to list a directory
func (h *FileSystemHandler) handleListDirectory(c *gin.Context, path string) {
	dir, err := h.ListDirectory(path)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error listing directory: %w", err))
		return
	}

	files := make([]map[string]interface{}, 0, len(dir.Files))
	for _, file := range dir.Files {
		files = append(files, map[string]interface{}{
			"path":         file.Path,
			"permissions":  file.Permissions,
			"size":         file.Size,
			"lastModified": file.LastModified,
			"owner":        file.Owner,
			"group":        file.Group,
		})
	}

	subdirs := make([]map[string]interface{}, 0, len(dir.Subdirectories))
	for _, subdir := range dir.Subdirectories {
		subdirs = append(subdirs, map[string]interface{}{
			"path": subdir.Path,
		})
	}

	h.SendJSON(c, http.StatusOK, gin.H{
		"path":           dir.Path,
		"files":          files,
		"subdirectories": subdirs,
	})
}

// HandleCreateOrUpdateFile handles PUT requests to /filesystem/:path
// @Summary Create or update file or directory
// @Description Create or update a file or directory
// @Tags filesystem
// @Accept json
// @Produce json
// @Param path path string true "File or directory path"
// @Param request body FileRequest true "File or directory information"
// @Success 200 {object} SuccessResponse "Success message"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/{path} [put]
func (h *FileSystemHandler) HandleCreateOrUpdateFile(c *gin.Context) {
	path, err := h.GetPathParam(c, "path")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}
	path, err = lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	var request struct {
		Content     string `json:"content"`
		IsDirectory bool   `json:"isDirectory"`
		Permissions string `json:"permissions"`
	}

	if err := h.BindJSON(c, &request); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Parse permissions or use default
	var permissions os.FileMode = 0644
	if request.Permissions != "" {
		permInt, err := strconv.ParseUint(request.Permissions, 8, 32)
		if err == nil {
			permissions = os.FileMode(permInt)
		}
	}

	if request.IsDirectory {
		// Create directory
		err := h.CreateDirectory(path, permissions)
		if err != nil {
			h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error creating directory: %w", err))
			return
		}
		h.SendSuccess(c, "Directory created successfully")
	} else {
		// Create or update file
		err := h.WriteFile(path, []byte(request.Content), permissions)
		if err != nil {
			h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error writing file: %w", err))
			return
		}
		h.SendSuccess(c, "File created/updated successfully")
	}
}

// HandleDeleteFileOrDirectory handles DELETE requests to /filesystem/:path
// @Summary Delete file or directory
// @Description Delete a file or directory
// @Tags filesystem
// @Accept json
// @Produce json
// @Param path path string true "File or directory path"
// @Param recursive query boolean false "Delete directory recursively"
// @Success 200 {object} SuccessResponse "Success message"
// @Failure 404 {object} ErrorResponse "File or directory not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/{path} [delete]
func (h *FileSystemHandler) HandleDeleteFile(c *gin.Context) {
	path, err := h.GetPathParam(c, "path")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}
	path, err = lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	recursive := c.Query("recursive")

	// Default to root if path is empty
	if path == "" {
		path = "/"
	}

	// Ensure path starts with a slash
	if path != "/" && len(path) > 0 && path[0] != '/' {
		path = "/" + path
	}

	// Check if it's a directory
	isDir, err := h.DirectoryExists(path)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	if isDir {
		// Delete directory
		err := h.DeleteDirectory(path, recursive == "true")
		if err != nil {
			h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error deleting directory: %w", err))
			return
		}
		h.SendSuccess(c, "Directory deleted successfully")
		return
	}

	// Check if it's a file
	isFile, err := h.FileExists(path)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	if isFile {
		// Delete file
		err := h.DeleteFile(path)
		if err != nil {
			h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error deleting file: %w", err))
			return
		}
		h.SendSuccess(c, "File deleted successfully")
		return
	}

	h.SendError(c, http.StatusNotFound, fmt.Errorf("file or directory not found"))
}

// HandleGetTree handles GET requests for directory trees
func (h *FileSystemHandler) HandleGetTree(c *gin.Context) {
	rootPath, exists := c.Get("rootPath")
	if !exists {
		// Fallback to path param if not set in context
		rootPath = c.Param("path")
	}

	// Convert to string
	rootPathStr, ok := rootPath.(string)
	if !ok {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("invalid path parameter"))
		return
	}

	rootPathStr, err := lib.FormatPath(rootPathStr)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Check if path exists and is a directory
	isDir, err := h.DirectoryExists(rootPathStr)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	if !isDir {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("path is not a directory"))
		return
	}

	// Get directory listing
	dir, err := h.ListDirectory(rootPathStr)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error getting file system tree: %w", err))
		return
	}

	h.SendJSON(c, http.StatusOK, dir)
}

// HandleCreateOrUpdateTree handles PUT requests for directory trees
func (h *FileSystemHandler) HandleCreateOrUpdateTree(c *gin.Context) {
	rootPath, exists := c.Get("rootPath")
	if !exists {
		// Fallback to path param if not set in context
		rootPath = c.Param("path")
	}

	// Convert to string
	rootPathStr, ok := rootPath.(string)
	if !ok {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("invalid path parameter"))
		return
	}
	rootPathStr, err := lib.FormatPath(rootPathStr)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	var request struct {
		Files map[string]string `json:"files"`
	}

	if err := h.BindJSON(c, &request); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Check if root path exists, create it if not
	isDir, err := h.DirectoryExists(rootPathStr)
	// The root path should be created if it doesn't exist
	if err != nil || !isDir {
		// Create the root directory if it doesn't exist or is not a directory
		err := h.CreateDirectory(rootPathStr, 0755)
		if err != nil {
			h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error creating root directory: %w", err))
			return
		}
	}

	// Process each file in the request
	for relativePath, content := range request.Files {
		// Combine root path with relative path, ensuring there's only one slash between them
		fullPath := rootPathStr
		if rootPathStr != "/" {
			fullPath += "/"
		}
		fullPath += relativePath

		// Get the parent directory path - we need to ensure it exists
		dir := filepath.Dir(fullPath)
		if dir != "/" {
			// Create parent directories
			err := h.CreateDirectory(dir, 0755)
			if err != nil {
				h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error creating parent directory: %w", err))
				return
			}
		}

		// Write the file
		err := h.WriteFile(fullPath, []byte(content), 0644)
		if err != nil {
			h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error writing file: %w", err))
			return
		}
	}

	// Get updated directory listing
	dir, err := h.ListDirectory(rootPathStr)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("error getting updated file system tree: %w", err))
		return
	}

	h.SendJSON(c, http.StatusOK, gin.H{
		"path":           dir.Path,
		"files":          dir.Files,
		"subdirectories": dir.Subdirectories,
		"message":        "Tree created/updated successfully",
	})
}

// HandleWatchDirectory streams file modification events for a directory
// @Summary Stream file modification events in a directory
// @Description Streams the path of modified files (one per line) in the given directory. Closes when the client disconnects.
// @Tags filesystem
// @Produce plain
// @Param path path string true "Directory path to watch"
// @Success 200 {string} string "Stream of modified file paths, one per line"
// @Failure 400 {object} ErrorResponse "Invalid path"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /watch/filesystem/{path} [get]
func (h *FileSystemHandler) HandleWatchDirectory(c *gin.Context) {
	path, err := h.GetPathParam(c, "path")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}
	path, err = lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	isDir, err := h.DirectoryExists(path)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}
	if !isDir {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("path is not a directory"))
		return
	}

	c.Writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("streaming not supported"))
		return
	}

	ctx := c.Request.Context()
	done := make(chan struct{})

	// Start watching the directory
	err = h.fs.WatchDirectory(path, func(event fsnotify.Event) {
		// Only send file events (not directory events)
		if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
			defer func() { _ = recover() }()
			if _, err := c.Writer.Write([]byte(event.Name + "\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	})
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	// Wait for client disconnect
	go func() {
		<-ctx.Done()
		close(done)
	}()

	<-done
}

// HandleWatchDirectoryWebSocket streams file modification events for a directory over WebSocket
// @Summary Stream file modification events in a directory via WebSocket
// @Description Streams JSON events of modified files in the given directory. Closes when the client disconnects.
// @Tags filesystem
// @Produce json
// @Param path path string true "Directory path to watch"
// @Success 101 {string} string "WebSocket connection established"
// @Failure 400 {object} ErrorResponse "Invalid path"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /ws/watch/filesystem/{path} [get]
func (h *FileSystemHandler) HandleWatchDirectoryWebSocket(c *gin.Context) {
	path, err := h.GetPathParam(c, "path")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}
	path, err = lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	isDir, err := h.DirectoryExists(path)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}
	if !isDir {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("path is not a directory"))
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	done := make(chan struct{})
	var once sync.Once

	err = h.fs.WatchDirectory(path, func(event fsnotify.Event) {
		if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
			msg := map[string]interface{}{
				"event": event.Op.String(),
				"name":  event.Name,
			}
			if err := conn.WriteJSON(msg); err != nil {
				once.Do(func() { close(done) })
			}
		}
	})
	if err != nil {
		conn.WriteJSON(map[string]string{"error": err.Error()})
		return
	}

	go func() {
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				once.Do(func() { close(done) })
				return
			}
		}
	}()

	<-done
}
