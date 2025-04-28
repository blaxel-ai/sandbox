package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/beamlit/uvm-api/src/handler/filesystem"
)

// fileSystemInstance is a singleton instance of the filesystem
var fileSystemInstance *filesystem.Filesystem

// getFileSystem returns the singleton filesystem instance
func getFileSystem() *filesystem.Filesystem {
	if fileSystemInstance == nil {
		// Use the root filesystem directly - no sandboxing
		fileSystemInstance = filesystem.NewFilesystem("/")
	}
	return fileSystemInstance
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
func HandleFileSystemRequest(c *gin.Context) {
	path := c.Param("path")
	fs := getFileSystem()

	// Default to root if path is empty
	if path == "" {
		path = "/"
	}

	// Ensure path starts with a slash
	if path != "/" && len(path) > 0 && path[0] != '/' {
		path = "/" + path
	}

	// Check if path is a directory
	isDir, err := fs.DirectoryExists(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if isDir {
		handleListDirectory(c, path)
		return
	}

	// Check if path is a file
	isFile, err := fs.FileExists(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if isFile {
		handleReadFile(c, path)
		return
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "File or directory not found"})
}

// handleReadFile handles requests to read a file
func handleReadFile(c *gin.Context, path string) {
	fs := getFileSystem()

	file, err := fs.ReadFile(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error reading file: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
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
func handleListDirectory(c *gin.Context, path string) {
	fs := getFileSystem()

	dir, err := fs.ListDirectory(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error listing directory: %v", err)})
		return
	}

	files := make([]map[string]interface{}, 0, len(dir.Files))
	for _, file := range dir.Files {
		files = append(files, map[string]interface{}{
			"path":         file.Path,
			"permissions":  file.Permissions.String(),
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

	c.JSON(http.StatusOK, gin.H{
		"path":           dir.Path,
		"files":          files,
		"subdirectories": subdirs,
	})
}

// HandleGetFileSystemTree handles GET requests to /filesystem/tree/*
// @Summary Get filesystem tree
// @Description Get a hierarchical listing of a directory
// @Tags filesystem
// @Accept json
// @Produce json
// @Param path path string true "Directory path"
// @Success 200 {object} filesystem.Directory "Directory tree"
// @Failure 400 {object} ErrorResponse "Path is not a directory"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/tree/{path} [get]
func HandleGetFileSystemTree(c *gin.Context) {
	rootPath, exists := c.Get("rootPath")
	if !exists {
		// Fallback to path param if not set in context
		rootPath = c.Param("path")
	}

	// Convert to string
	rootPathStr, ok := rootPath.(string)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid path parameter"})
		return
	}

	fs := getFileSystem()

	// Default to root if path is not provided or empty
	if rootPathStr == "" {
		rootPathStr = "/"
	}

	// Ensure rootPath starts with a slash
	if rootPathStr != "/" && len(rootPathStr) > 0 && rootPathStr[0] != '/' {
		rootPathStr = "/" + rootPathStr
	}

	// Check if path exists and is a directory
	isDir, err := fs.DirectoryExists(rootPathStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !isDir {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Path is not a directory"})
		return
	}

	// Get directory listing
	dir, err := fs.ListDirectory(rootPathStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error getting file system tree: %v", err)})
		return
	}

	c.JSON(http.StatusOK, dir)
}

// HandleCreateOrUpdateTree handles PUT requests to /filesystem/tree/*
// @Summary Create or update filesystem tree
// @Description Create or update multiple files in a directory
// @Tags filesystem
// @Accept json
// @Produce json
// @Param path path string true "Base directory path"
// @Param request body TreeRequest true "Files to create or update"
// @Success 200 {object} DirectoryResponse "Updated tree"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/tree/{path} [put]
func HandleCreateOrUpdateTree(c *gin.Context) {
	rootPath, exists := c.Get("rootPath")
	if !exists {
		// Fallback to path param if not set in context
		rootPath = c.Param("path")
	}

	// Convert to string
	rootPathStr, ok := rootPath.(string)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid path parameter"})
		return
	}

	fs := getFileSystem()

	// Default to root if path is empty
	if rootPathStr == "" {
		rootPathStr = "/"
	}

	// Ensure rootPath starts with a slash
	if rootPathStr != "/" && len(rootPathStr) > 0 && rootPathStr[0] != '/' {
		rootPathStr = "/" + rootPathStr
	}

	var request struct {
		Files map[string]string `json:"files"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if root path exists, create it if not
	isDir, err := fs.DirectoryExists(rootPathStr)
	// The root path should be created if it doesn't exist
	if err != nil || !isDir {
		// Create the root directory if it doesn't exist or is not a directory
		err := fs.CreateDirectory(rootPathStr, 0755)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating root directory: %v", err)})
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
			err := fs.CreateDirectory(dir, 0755)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating parent directory: %v", err)})
				return
			}
		}

		// Write the file
		err := fs.WriteFile(fullPath, []byte(content), 0644)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error writing file: %v", err)})
			return
		}
	}

	// Get updated directory listing
	dir, err := fs.ListDirectory(rootPathStr)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error getting updated file system tree: %v", err)})
		return
	}

	// Add success message to response
	c.JSON(http.StatusOK, gin.H{
		"path":           dir.Path,
		"files":          dir.Files,
		"subdirectories": dir.Subdirectories,
		"message":        "Tree created/updated successfully",
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
func HandleCreateOrUpdateFile(c *gin.Context) {
	path := c.Param("path")
	fs := getFileSystem()

	// Default to root if path is empty
	if path == "" {
		path = "/"
	}

	// Ensure path starts with a slash
	if path != "/" && len(path) > 0 && path[0] != '/' {
		path = "/" + path
	}

	var request struct {
		Content     string `json:"content"`
		IsDirectory bool   `json:"isDirectory"`
		Permissions string `json:"permissions"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		err := fs.CreateDirectory(path, permissions)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating directory: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"path": path, "message": "Directory created successfully"})
	} else {
		// Create or update file
		err := fs.WriteFile(path, []byte(request.Content), permissions)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error writing file: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"path": path, "message": "File created/updated successfully"})
	}
}

// HandleDeleteFileOrDirectory handles DELETE requests to /filesystem/:path
// @Summary Delete file or directory
// @Description Delete a file or directory
// @Tags filesystem
// @Accept json
// @Produce json
// @Param path path string true "File or directory path"
// @Param recursive body boolean false "Delete directory recursively"
// @Success 200 {object} SuccessResponse "Success message"
// @Failure 404 {object} ErrorResponse "File or directory not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/{path} [delete]
func HandleDeleteFileOrDirectory(c *gin.Context) {
	path := c.Param("path")
	fs := getFileSystem()

	// Default to root if path is empty
	if path == "" {
		path = "/"
	}

	// Ensure path starts with a slash
	if path != "/" && len(path) > 0 && path[0] != '/' {
		path = "/" + path
	}

	var request struct {
		Recursive bool `json:"recursive"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		// If JSON is not provided, default to non-recursive
		request.Recursive = false
	}

	// Check if it's a directory
	isDir, err := fs.DirectoryExists(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if isDir {
		// Delete directory
		err := fs.DeleteDirectory(path, request.Recursive)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error deleting directory: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"path": path, "message": "Directory deleted successfully"})
		return
	}

	// Check if it's a file
	isFile, err := fs.FileExists(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if isFile {
		// Delete file
		err := fs.DeleteFile(path)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error deleting file: %v", err)})
			return
		}
		c.JSON(http.StatusOK, gin.H{"path": path, "message": "File deleted successfully"})
		return
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "File or directory not found"})
}
