package handler

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/gin-gonic/gin"
)

// CopyRequest describes a recursive copy, with `cp -r` placement.
type CopyRequest struct {
	Source      string `json:"source" binding:"required" example:"/app/source"`
	Destination string `json:"destination" binding:"required" example:"/app/destination"`
	NoOverwrite bool   `json:"noOverwrite" example:"false" default:"false"`
} // @name CopyRequest

type CopyResponse struct {
	Source      string `json:"source" binding:"required"`
	Destination string `json:"destination" binding:"required"`
	Message     string `json:"message" binding:"required"`
} // @name CopyResponse

// HandleCopy copies files or directories.
// @Summary Copy a file or directory
// @Description Recursively copy a file or directory in one request, placed like `cp -r`: when destination is an existing directory (or a symlink to one), the copy goes inside it, named after source; otherwise destination is the copy's path. Relative paths use the filesystem working directory. Source symlinks are copied as symlinks; special files are refused. By default existing files are overwritten and existing directories merged.
// @Description
// @Description With noOverwrite=true, every entry is created exclusively (O_EXCL, mkdir, symlink), with no check-then-write race: an existing final target, including an empty directory or a dangling symlink, returns 409 with code FILE_ALREADY_EXISTS and is left unchanged. A copy is not a transaction: on a later failure, entries it already created remain.
// @Tags filesystem
// @Accept json
// @Produce json
// @Param request body CopyRequest true "Source, destination, and optional noOverwrite flag"
// @Success 200 {object} CopyResponse "Files copied"
// @Failure 400 {object} ErrorResponse "Bad request"
// @Failure 409 {object} ErrorResponse "Destination exists (FILE_ALREADY_EXISTS)"
// @Failure 422 {object} ErrorResponse "Copy failed"
// @Router /filesystem-copy [post]
func (h *FileSystemHandler) HandleCopy(c *gin.Context) {
	var request CopyRequest
	if err := h.BindJSON(c, &request); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}
	if err := h.fs.Copy(request.Source, request.Destination, filesystem.WriteOptions{NoOverwrite: request.NoOverwrite}); err != nil {
		h.sendFilesystemError(c, http.StatusUnprocessableEntity, fmt.Errorf("error copying destination %s: %w", request.Destination, err))
		return
	}
	h.SendJSON(c, http.StatusOK, CopyResponse{
		Source: request.Source, Destination: request.Destination, Message: "Files copied",
	})
}

// Preserve existing error statuses except for exclusive-create conflicts.
// errors.Is recognizes wrapped PathErrors (unlike os.IsExist).
func (h *FileSystemHandler) sendFilesystemError(c *gin.Context, status int, err error) {
	if errors.Is(err, os.ErrExist) {
		c.JSON(http.StatusConflict, ErrorResponse{Error: err.Error(), Code: "FILE_ALREADY_EXISTS"})
		return
	}
	h.SendError(c, status, err)
}
