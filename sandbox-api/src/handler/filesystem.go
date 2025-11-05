package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/blaxel-ai/sandbox-api/src/lib"
)

// FileSystemHandler handles filesystem operations
type FileSystemHandler struct {
	*BaseHandler
	fs               *filesystem.Filesystem
	multipartManager *filesystem.MultipartManager
}

// FileEvent represents a file event
type FileEvent struct {
	Op    string  `json:"op"`
	Name  string  `json:"name"`
	Path  string  `json:"path"`
	Error *string `json:"error"`
} // @name FileEvent

// FileRequest represents the request body for creating or updating a file
type FileRequest struct {
	Content     string `json:"content" example:"file contents here"`
	IsDirectory bool   `json:"isDirectory" example:"false"`
	Permissions string `json:"permissions" example:"0644"`
} // @name FileRequest

// MultipartInitiateRequest represents the request body for initiating a multipart upload
type MultipartInitiateRequest struct {
	Permissions string `json:"permissions" example:"0644"`
} // @name MultipartInitiateRequest

// MultipartInitiateResponse represents the response after initiating a multipart upload
type MultipartInitiateResponse struct {
	UploadID string `json:"uploadId" example:"550e8400-e29b-41d4-a716-446655440000"`
	Path     string `json:"path" example:"/tmp/largefile.dat"`
} // @name MultipartInitiateResponse

// MultipartUploadPartResponse represents the response after uploading a part
type MultipartUploadPartResponse struct {
	PartNumber int    `json:"partNumber" example:"1"`
	ETag       string `json:"etag" example:"5d41402abc4b2a76b9719d911017c592"`
	Size       int64  `json:"size" example:"5242880"`
} // @name MultipartUploadPartResponse

// MultipartPartInfo represents a single part in the complete request
type MultipartPartInfo struct {
	PartNumber int    `json:"partNumber" example:"1"`
	ETag       string `json:"etag" example:"5d41402abc4b2a76b9719d911017c592"`
} // @name MultipartPartInfo

// MultipartCompleteRequest represents the request body for completing a multipart upload
type MultipartCompleteRequest struct {
	Parts []MultipartPartInfo `json:"parts"`
} // @name MultipartCompleteRequest

// MultipartListPartsResponse represents the response when listing parts
type MultipartListPartsResponse struct {
	UploadID string                    `json:"uploadId" example:"550e8400-e29b-41d4-a716-446655440000"`
	Parts    []filesystem.UploadedPart `json:"parts"`
} // @name MultipartListPartsResponse

// MultipartListUploadsResponse represents the response when listing all uploads
type MultipartListUploadsResponse struct {
	Uploads []*filesystem.MultipartUpload `json:"uploads"`
} // @name MultipartListUploadsResponse

// NewFileSystemHandler creates a new filesystem handler
func NewFileSystemHandler() *FileSystemHandler {
	// Get working directory from environment or use default
	workingDir := os.Getenv("WORKDIR")
	if workingDir == "" {
		// Try to get current working directory
		if cwd, err := os.Getwd(); err == nil {
			workingDir = cwd
		} else {
			// Default to / if we can't get the current directory
			workingDir = "/"
		}
	}

	// Setup multipart uploads directory
	uploadsDir := filepath.Join(os.TempDir(), "multipart-uploads")
	multipartManager := filesystem.NewMultipartManager(uploadsDir)

	// Load existing uploads from disk
	if multipartManager != nil {
		_ = multipartManager.LoadUploads()
	}

	return &FileSystemHandler{
		BaseHandler:      NewBaseHandler(),
		fs:               filesystem.NewFilesystemWithWorkingDir("/", workingDir),
		multipartManager: multipartManager,
	}
}

// extractPathFromRequest extracts the path from the request and determines if it's relative or absolute
func (h *FileSystemHandler) extractPathFromRequest(c *gin.Context) string {
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
		// Special case: /filesystem/ means current directory
		return "."
	} else if strings.HasPrefix(path, "/") {
		// Remove leading slash for relative paths like /src -> src
		return path[1:]
	}

	return path
}

// GetWorkingDirectory gets the current working directory
func (h *FileSystemHandler) GetWorkingDirectory() (string, error) {
	return h.fs.WorkingDir, nil
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
// For files, the response format depends on the Accept header:
// - Accept: application/json returns JSON with file metadata and content
// - Accept: application/octet-stream returns raw file content for download
// - download=true query parameter forces download mode
// @Summary Get file or directory information
// @Description Get content of a file or listing of a directory. Use Accept header to control response format for files.
// @Tags filesystem
// @Accept json
// @Produce json,octet-stream
// @Param path path string true "File or directory path"
// @Param download query boolean false "Force download mode for files"
// @Success 200 {file} file "File content (download mode)"
// @Success 200 {object} filesystem.FileWithContent "File content (JSON mode)"
// @Success 200 {object} filesystem.Directory "Directory listing"
// @Failure 404 {object} ErrorResponse "File or directory not found"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/{path} [get]
func (h *FileSystemHandler) HandleGetFile(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Check if path is a directory
	isDir, err := h.DirectoryExists(path)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	if isDir {
		h.handleListDirectory(c, path)
		return
	}

	// Check if path is a file
	isFile, err := h.FileExists(path)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, err)
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
	// Check if client wants to download the file content directly
	// This is determined by the Accept header
	acceptHeader := c.GetHeader("Accept")
	wantsDownload := false

	// Check if client explicitly requests non-JSON response
	if acceptHeader != "" {
		// If Accept header explicitly contains application/octet-stream, treat as download
		if strings.Contains(acceptHeader, "application/octet-stream") {
			wantsDownload = true
		}
		// If Accept header doesn't contain application/json AND doesn't contain */* (accept all), treat as download
		// This handles specific content types like text/plain, text/html, etc.
		if !strings.Contains(acceptHeader, "application/json") &&
			!strings.Contains(acceptHeader, "*/*") &&
			!strings.Contains(acceptHeader, "application/octet-stream") {
			wantsDownload = true
		}
	}
	// Default behavior: If no Accept header or Accept: */*, default to JSON

	// Also support explicit query parameter for download
	if c.Query("download") == "true" {
		wantsDownload = true
	}

	if wantsDownload {
		// Stream binary content directly from disk (no memory buffering)
		absPath, err := h.fs.GetAbsolutePath(path)
		if err != nil {
			h.SendError(c, http.StatusBadRequest, err)
			return
		}

		// Get file info for headers
		info, err := os.Stat(absPath)
		if err != nil {
			h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error reading file: %w", err))
			return
		}

		if info.IsDir() {
			h.SendError(c, http.StatusBadRequest, fmt.Errorf("path is a directory, not a file"))
			return
		}

		filename := filepath.Base(path)

		// Set appropriate Content-Type based on file extension
		contentType := "application/octet-stream"
		ext := filepath.Ext(filename)
		switch ext {
		case ".txt", ".log":
			contentType = "text/plain"
		case ".html", ".htm":
			contentType = "text/html"
		case ".css":
			contentType = "text/css"
		case ".js":
			contentType = "application/javascript"
		case ".json":
			contentType = "application/json"
		case ".xml":
			contentType = "application/xml"
		case ".pdf":
			contentType = "application/pdf"
		case ".zip":
			contentType = "application/zip"
		case ".tar":
			contentType = "application/x-tar"
		case ".gz":
			contentType = "application/gzip"
		case ".jpg", ".jpeg":
			contentType = "image/jpeg"
		case ".png":
			contentType = "image/png"
		case ".gif":
			contentType = "image/gif"
		case ".svg":
			contentType = "image/svg+xml"
		}

		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
		c.Header("Content-Type", contentType)
		c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))

		// Open file and stream directly to response
		file, err := os.Open(absPath)
		if err != nil {
			h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error opening file: %w", err))
			return
		}
		defer file.Close()

		// Stream file content directly to HTTP response (no memory buffering)
		c.Status(http.StatusOK)
		if _, err := io.Copy(c.Writer, file); err != nil {
			logrus.Errorf("Error streaming file: %v", err)
			return
		}
		return
	}

	// JSON mode: read entire file into memory for serialization
	file, err := h.ReadFile(path)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error reading file: %w", err))
		return
	}

	// Default behavior: return JSON response
	h.SendJSON(c, http.StatusOK, file)
}

// handleListDirectory handles requests to list a directory
func (h *FileSystemHandler) handleListDirectory(c *gin.Context, path string) {
	dir, err := h.ListDirectory(path)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error listing directory: %w", err))
		return
	}

	h.SendJSON(c, http.StatusOK, dir)
}

// HandleCreateOrUpdateFile handles PUT requests to /filesystem/:path
// @Summary Create or update a file or directory
// @Description Create or update a file or directory
// @Tags filesystem
// @Accept json
// @Produce json
// @Param path path string true "File or directory path"
// @Param request body FileRequest true "File or directory details"
// @Success 200 {object} SuccessResponse "Success message"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/{path} [put]
func (h *FileSystemHandler) HandleCreateOrUpdateFile(c *gin.Context) {
	contentType := c.GetHeader("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		h.HandleCreateOrUpdateBinary(c)
	} else {
		h.HandleCreateOrUpdateFileJSON(c)
	}
}

func (h *FileSystemHandler) HandleCreateOrUpdateFileJSON(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
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

	// Parse permissions or use appropriate defaults
	var permissions os.FileMode
	if request.Permissions != "" {
		permInt, err := strconv.ParseUint(request.Permissions, 8, 32)
		if err != nil {
			h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid permissions format '%s': %w", request.Permissions, err))
			return
		}
		permissions = os.FileMode(permInt)
	} else {
		// Use appropriate defaults: 0755 for directories, 0644 for files
		if request.IsDirectory {
			permissions = 0755
		} else {
			permissions = 0644
		}
	}

	// Handle directory creation
	if request.IsDirectory {
		// Directories need different default permissions than files
		if request.Permissions == "" {
			permissions = 0755
		}
		if err := h.CreateDirectory(path, permissions); err != nil {
			h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error creating directory: %w", err))
			return
		}
		h.SendSuccessWithPath(c, path, "Directory created successfully")
		return
	}

	// Handle file creation/update
	if err := h.WriteFile(path, []byte(request.Content), permissions); err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error writing file: %w", err))
		return
	}

	h.SendSuccessWithPath(c, path, "File created/updated successfully")
}

func (h *FileSystemHandler) HandleCreateOrUpdateBinary(c *gin.Context) {
	// Get path from form data
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Use streaming multipart reader to avoid extra buffering/copies
	mr, err := c.Request.MultipartReader()
	if err != nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("error reading multipart data: %w", err))
		return
	}

	var permissions os.FileMode = 0644
	var wroteFile bool

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.SendError(c, http.StatusBadRequest, fmt.Errorf("error reading multipart part: %w", err))
			return
		}

		name := part.FormName()
		filename := part.FileName()
		if name == "permissions" && filename == "" {
			// read small permission value
			data, _ := io.ReadAll(part)
			if len(data) > 0 {
				permInt, perr := strconv.ParseUint(strings.TrimSpace(string(data)), 8, 32)
				if perr != nil {
					_ = part.Close()
					h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid permissions format '%s': %w", strings.TrimSpace(string(data)), perr))
					return
				}
				permissions = os.FileMode(permInt)
			}
			_ = part.Close()
			continue
		}

		if name == "file" && filename != "" && !wroteFile {
			// Stream directly to disk with requested permissions
			if err := h.fs.WriteFileFromReader(path, part, permissions); err != nil {
				_ = part.Close()
				h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error writing binary file: %w", err))
				return
			}
			wroteFile = true
			_ = part.Close()
			continue
		}

		// Close any unhandled parts
		_ = part.Close()
	}

	if !wroteFile {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("missing 'file' field in multipart form"))
		return
	}

	h.SendSuccessWithPath(c, path, "Binary file uploaded successfully")
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
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/{path} [delete]
func (h *FileSystemHandler) HandleDeleteFile(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	recursive := c.Query("recursive")

	// Check if it's a directory
	isDir, err := h.DirectoryExists(path)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	if isDir {
		// Delete directory
		err := h.DeleteDirectory(path, recursive == "true")
		if err != nil {
			h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error deleting directory: %w", err))
			return
		}
		h.SendSuccessWithPath(c, path, "Directory deleted successfully")
		return
	}

	// Check if it's a file
	isFile, err := h.FileExists(path)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	if isFile {
		// Delete file
		err := h.DeleteFile(path)
		if err != nil {
			h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error deleting file: %w", err))
			return
		}
		h.SendSuccessWithPath(c, path, "File deleted successfully")
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
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("invalid path parameter"))
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
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	if !isDir {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("path is not a directory"))
		return
	}

	// Get directory listing
	dir, err := h.ListDirectory(rootPathStr)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error getting file system tree: %w", err))
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
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("invalid path parameter"))
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

	// Create the root directory if it doesn't exist
	isDir, err := h.DirectoryExists(rootPathStr)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	if !isDir {
		if err := h.CreateDirectory(rootPathStr, 0755); err != nil {
			h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error creating root directory: %w", err))
			return
		}
	}

	// Create files
	for filePath, content := range request.Files {
		// Get the absolute path of the file
		absPath := filepath.Join(rootPathStr, filePath)

		// Create parent directories if they don't exist
		parentDir := filepath.Dir(absPath)
		isDir, err := h.DirectoryExists(parentDir)
		if err != nil {
			h.SendError(c, http.StatusUnprocessableEntity, err)
			return
		}

		if !isDir {
			if err := h.CreateDirectory(parentDir, 0755); err != nil {
				h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error creating parent directory: %w", err))
				return
			}
		}

		// Write the file
		if err := h.WriteFile(absPath, []byte(content), 0644); err != nil {
			h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error writing file: %w", err))
			return
		}
	}

	// Get updated tree
	dir, err := h.ListDirectory(rootPathStr)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error getting updated file system tree: %w", err))
		return
	}

	h.SendJSON(c, http.StatusOK, dir)
}

// HandleDeleteTree handles DELETE requests for directory trees
func (h *FileSystemHandler) HandleDeleteTree(c *gin.Context) {
	rootPath, exists := c.Get("rootPath")
	if !exists {
		// Fallback to path param if not set in context
		rootPath = c.Param("path")
	}

	// Convert to string
	rootPathStr, ok := rootPath.(string)
	if !ok {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("invalid path parameter"))
		return
	}

	recursive := c.Query("recursive") == "true"

	// Delete the directory
	if err := h.DeleteDirectory(rootPathStr, recursive); err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("error deleting directory: %w", err))
		return
	}

	h.SendSuccessWithPath(c, rootPathStr, "Directory deleted successfully")
}

// HandleInitiateMultipartUpload initiates a multipart upload
// @Summary Initiate multipart upload
// @Description Initiate a multipart upload session for a file
// @Tags filesystem
// @Accept json
// @Produce json
// @Param path path string true "File path"
// @Param request body MultipartInitiateRequest false "Optional permissions"
// @Success 200 {object} MultipartInitiateResponse "Upload session created"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem-multipart/initiate/{path} [post]
func (h *FileSystemHandler) HandleInitiateMultipartUpload(c *gin.Context) {
	if h.multipartManager == nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("multipart upload not available"))
		return
	}

	path := h.extractPathFromRequest(c)
	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Get absolute path for final destination
	absPath, err := h.fs.GetAbsolutePath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Parse optional permissions
	var request MultipartInitiateRequest
	_ = h.BindJSON(c, &request)

	var permissions os.FileMode = 0644
	if request.Permissions != "" {
		permInt, err := strconv.ParseUint(request.Permissions, 8, 32)
		if err != nil {
			h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid permissions format: %w", err))
			return
		}
		permissions = os.FileMode(permInt)
	}

	upload, err := h.multipartManager.InitiateUpload(absPath, permissions)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("failed to initiate upload: %w", err))
		return
	}

	response := MultipartInitiateResponse{
		UploadID: upload.UploadID,
		Path:     absPath,
	}
	h.SendJSON(c, http.StatusOK, response)
}

// HandleUploadPart uploads a single part of a multipart upload
// @Summary Upload part
// @Description Upload a single part of a multipart upload
// @Tags filesystem
// @Accept multipart/form-data
// @Produce json
// @Param uploadId path string true "Upload ID"
// @Param partNumber query int true "Part number (1-10000)"
// @Param file formData file true "Part data"
// @Success 200 {object} MultipartUploadPartResponse "Part uploaded"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 404 {object} ErrorResponse "Upload not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem-multipart/{uploadId}/part [put]
func (h *FileSystemHandler) HandleUploadPart(c *gin.Context) {
	if h.multipartManager == nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("multipart upload not available"))
		return
	}

	uploadID := c.Param("uploadId")
	if uploadID == "" {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("uploadId is required"))
		return
	}

	partNumberStr := c.Query("partNumber")
	if partNumberStr == "" {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("partNumber is required"))
		return
	}

	partNumber, err := strconv.Atoi(partNumberStr)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid partNumber: %w", err))
		return
	}

	// Use streaming multipart reader
	mr, err := c.Request.MultipartReader()
	if err != nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("error reading multipart data: %w", err))
		return
	}

	var uploadedPart *filesystem.UploadedPart
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			h.SendError(c, http.StatusBadRequest, fmt.Errorf("error reading multipart part: %w", err))
			return
		}

		if part.FormName() == "file" {
			uploadedPart, err = h.multipartManager.UploadPart(uploadID, partNumber, part)
			_ = part.Close()
			if err != nil {
				h.SendError(c, http.StatusInternalServerError, fmt.Errorf("failed to upload part: %w", err))
				return
			}
			break
		}
		_ = part.Close()
	}

	if uploadedPart == nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("missing 'file' field in multipart form"))
		return
	}

	response := MultipartUploadPartResponse{
		PartNumber: uploadedPart.PartNumber,
		ETag:       uploadedPart.ETag,
		Size:       uploadedPart.Size,
	}
	h.SendJSON(c, http.StatusOK, response)
}

// HandleCompleteMultipartUpload completes a multipart upload
// @Summary Complete multipart upload
// @Description Complete a multipart upload by assembling all parts
// @Tags filesystem
// @Accept json
// @Produce json
// @Param uploadId path string true "Upload ID"
// @Param request body MultipartCompleteRequest true "List of parts"
// @Success 200 {object} SuccessResponse "Upload completed"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 404 {object} ErrorResponse "Upload not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem-multipart/{uploadId}/complete [post]
func (h *FileSystemHandler) HandleCompleteMultipartUpload(c *gin.Context) {
	if h.multipartManager == nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("multipart upload not available"))
		return
	}

	uploadID := c.Param("uploadId")
	if uploadID == "" {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("uploadId is required"))
		return
	}

	var request MultipartCompleteRequest
	if err := h.BindJSON(c, &request); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	if len(request.Parts) == 0 {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("at least one part is required"))
		return
	}

	// Get upload metadata to get the path
	upload, err := h.multipartManager.GetUpload(uploadID)
	if err != nil {
		h.SendError(c, http.StatusNotFound, err)
		return
	}

	// Convert MultipartPartInfo to UploadedPart for the manager
	parts := make([]filesystem.UploadedPart, len(request.Parts))
	for i, p := range request.Parts {
		parts[i] = filesystem.UploadedPart{
			PartNumber: p.PartNumber,
			ETag:       p.ETag,
		}
	}

	if err := h.multipartManager.CompleteUpload(uploadID, parts); err != nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("failed to complete upload: %w", err))
		return
	}

	h.SendSuccessWithPath(c, upload.Path, "Multipart upload completed successfully")
}

// HandleAbortMultipartUpload aborts a multipart upload
// @Summary Abort multipart upload
// @Description Abort a multipart upload and clean up all parts
// @Tags filesystem
// @Produce json
// @Param uploadId path string true "Upload ID"
// @Success 200 {object} SuccessResponse "Upload aborted"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 404 {object} ErrorResponse "Upload not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem-multipart/{uploadId}/abort [delete]
func (h *FileSystemHandler) HandleAbortMultipartUpload(c *gin.Context) {
	if h.multipartManager == nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("multipart upload not available"))
		return
	}

	uploadID := c.Param("uploadId")
	if uploadID == "" {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("uploadId is required"))
		return
	}

	if err := h.multipartManager.AbortUpload(uploadID); err != nil {
		h.SendError(c, http.StatusNotFound, err)
		return
	}

	h.SendSuccess(c, "Multipart upload aborted successfully")
}

// HandleListParts lists all uploaded parts for a multipart upload
// @Summary List parts
// @Description List all uploaded parts for a multipart upload
// @Tags filesystem
// @Produce json
// @Param uploadId path string true "Upload ID"
// @Success 200 {object} MultipartListPartsResponse "List of parts"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 404 {object} ErrorResponse "Upload not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem-multipart/{uploadId}/parts [get]
func (h *FileSystemHandler) HandleListParts(c *gin.Context) {
	if h.multipartManager == nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("multipart upload not available"))
		return
	}

	uploadID := c.Param("uploadId")
	if uploadID == "" {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("uploadId is required"))
		return
	}

	parts, err := h.multipartManager.ListParts(uploadID)
	if err != nil {
		h.SendError(c, http.StatusNotFound, err)
		return
	}

	// Convert pointers to values for the response
	partsList := make([]filesystem.UploadedPart, len(parts))
	for i, p := range parts {
		partsList[i] = *p
	}

	response := MultipartListPartsResponse{
		UploadID: uploadID,
		Parts:    partsList,
	}
	h.SendJSON(c, http.StatusOK, response)
}

// HandleListMultipartUploads lists all active multipart uploads
// @Summary List multipart uploads
// @Description List all active multipart uploads
// @Tags filesystem
// @Produce json
// @Success 200 {object} MultipartListUploadsResponse "List of active uploads"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem-multipart [get]
func (h *FileSystemHandler) HandleListMultipartUploads(c *gin.Context) {
	if h.multipartManager == nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("multipart upload not available"))
		return
	}

	uploads := h.multipartManager.ListUploads()

	response := MultipartListUploadsResponse{
		Uploads: uploads,
	}
	h.SendJSON(c, http.StatusOK, response)
}

// HandleWatchDirectory streams file modification events for a directory
// @Summary Stream file modification events in a directory
// @Description Streams the path of modified files (one per line) in the given directory. Closes when the client disconnects.
// @Tags filesystem
// @Produce plain
// @Param ignore query string false "Ignore patterns (comma-separated)"
// @Param path path string true "Directory path to watch"
// @Success 200 {string} string "Stream of modified file paths, one per line"
// @Failure 400 {object} ErrorResponse "Invalid path"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /watch/filesystem/{path} [get]
func (h *FileSystemHandler) HandleWatchDirectory(c *gin.Context) {
	path := h.extractPathFromRequest(c)

	path, err := lib.FormatPath(path)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Parse ignore patterns from query param
	ignoreParam := c.Query("ignore")
	var ignorePatterns []string
	if ignoreParam != "" {
		ignorePatterns = strings.Split(ignoreParam, ",")
	}
	shouldIgnore := func(eventPath string) bool {
		for _, pattern := range ignorePatterns {
			if pattern != "" && strings.Contains(eventPath, pattern) {
				return true
			}
		}
		return false
	}

	recursive := false
	if strings.HasSuffix(path, "/**") {
		recursive = true
		path = strings.TrimSuffix(path, "/**")
		if path == "" {
			path = "/"
		}
	}

	isDir, err := h.DirectoryExists(path)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, err)
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

	var stop func()
	if recursive {
		stop, err = h.fs.WatchDirectoryRecursive(path, func(event fsnotify.Event) {
			defer func() { _ = recover() }()
			if shouldIgnore(event.Name) {
				return
			}
			msg := FileEvent{
				Op:    event.Op.String(),
				Name:  strings.Split(event.Name, "/")[len(strings.Split(event.Name, "/"))-1],
				Path:  strings.Join(strings.Split(event.Name, "/")[:len(strings.Split(event.Name, "/"))-1], "/"),
				Error: nil,
			}
			json, err := json.Marshal(msg)
			if err != nil {
				logrus.Error("Error marshalling file event:", err)
				h.SendError(c, http.StatusInternalServerError, err)
				return
			}
			if _, err := c.Writer.Write([]byte(string(json) + "\n")); err != nil {
				return
			}
			flusher.Flush()
		})
	} else {
		stop, err = h.fs.WatchDirectory(path, func(event fsnotify.Event) {
			defer func() { _ = recover() }()
			if shouldIgnore(event.Name) {
				return
			}
			msg := FileEvent{
				Op:    event.Op.String(),
				Name:  strings.Split(event.Name, "/")[len(strings.Split(event.Name, "/"))-1],
				Path:  strings.Join(strings.Split(event.Name, "/")[:len(strings.Split(event.Name, "/"))-1], "/"),
				Error: nil,
			}
			json, err := json.Marshal(msg)
			if err != nil {
				logrus.Error("Error marshalling file event:", err)
				h.SendError(c, http.StatusInternalServerError, err)
				return
			}
			if _, err := c.Writer.Write([]byte(string(json) + "\n")); err != nil {
				return
			}
			flusher.Flush()
		})
	}
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}
	defer stop() // Ensures watcher is removed when handler exits

	// Keepalive ticker to prevent idle timeouts while watching
	keepaliveTicker := time.NewTicker(30 * time.Second)
	defer keepaliveTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				close(done)
				return
			case <-keepaliveTicker.C:
				// Send a keepalive line
				if _, err := c.Writer.Write([]byte("[keepalive]\n")); err != nil {
					close(done)
					return
				}
				flusher.Flush()
			}
		}
	}()

	<-done
}
