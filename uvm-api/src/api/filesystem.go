package api

import (
	"fmt"
	"net/http"
	"os"
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
// @Param recursive query boolean false "Delete directory recursively"
// @Success 200 {object} SuccessResponse "Success message"
// @Failure 404 {object} ErrorResponse "File or directory not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /filesystem/{path} [delete]
func HandleDeleteFileOrDirectory(c *gin.Context) {
	path := c.Param("path")
	fs := getFileSystem()
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
	isDir, err := fs.DirectoryExists(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if isDir {
		// Delete directory
		err := fs.DeleteDirectory(path, recursive == "true")
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
