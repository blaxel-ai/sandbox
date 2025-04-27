package api

// These are models specifically for Swagger documentation

// FileRequest represents the request body for creating or updating a file
type FileRequest struct {
	Content     string `json:"content" example:"file contents here"`
	IsDirectory bool   `json:"isDirectory" example:"false"`
	Permissions string `json:"permissions" example:"0644"`
}

// FileResponse represents a file in the filesystem
type FileResponse struct {
	Path         string `json:"path" example:"/path/to/file.txt"`
	Content      string `json:"content,omitempty" example:"file contents here"`
	Permissions  string `json:"permissions" example:"0644"`
	Size         int64  `json:"size" example:"1024"`
	LastModified int64  `json:"lastModified" example:"1627984000"`
	Owner        string `json:"owner" example:"root"`
	Group        string `json:"group" example:"wheel"`
}

// DirectoryResponse represents a directory in the filesystem
type DirectoryResponse struct {
	Path           string                 `json:"path" example:"/path/to/dir"`
	Files          []FileResponse         `json:"files"`
	Subdirectories []SubdirectoryResponse `json:"subdirectories"`
}

// SubdirectoryResponse represents a subdirectory in a directory listing
type SubdirectoryResponse struct {
	Path string `json:"path" example:"/path/to/subdir"`
}

// TreeRequest represents a request to create or update a file tree
type TreeRequest struct {
	Files map[string]string `json:"files" example:"file1.txt:content1,dir/file2.txt:content2"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error" example:"Error message"`
}

// SuccessResponse represents a success response
type SuccessResponse struct {
	Path    string `json:"path" example:"/path/to/file"`
	Message string `json:"message" example:"File created successfully"`
}
