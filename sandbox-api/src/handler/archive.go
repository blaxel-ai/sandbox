package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/handler/archive"
)

// ArchiveHandler exports the sandbox's filesystem changes so the sandbox can be
// destroyed and restored later from the same base image.
type ArchiveHandler struct {
	*BaseHandler
}

// NewArchiveHandler creates a new archive handler.
func NewArchiveHandler() *ArchiveHandler {
	archive.APIVersion = Version
	return &ArchiveHandler{BaseHandler: NewBaseHandler()}
}

// HandleExport handles POST requests to /archive/export
// @Summary Export the filesystem changes to a presigned URL
// @Description Archives everything the sandbox changed on top of its base image and streams it, uncompressed, to a presigned S3 PUT URL. The memory of the sandbox is not archived.
// @Description The sandbox is quiesced first: the process list is saved (unless saveProcesses is false), every process is stopped, and the API then refuses the calls that would write to the filesystem. The freeze is not lifted afterwards, since an exported sandbox is meant to be restored elsewhere; call POST /archive/resume to lift it.
// @Description Use dryRun to get the archive's content and exact size without stopping anything and without uploading.
// @Tags archive
// @Accept json
// @Produce json
// @Param request body ExportOptions true "Export options"
// @Success 200 {object} ExportResult "Export result"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 409 {object} ErrorResponse "An export is already in progress"
// @Failure 500 {object} ErrorResponse "Export failed"
// @Router /archive/export [post]
func (h *ArchiveHandler) HandleExport(c *gin.Context) {
	var options archive.ExportOptions
	if err := h.BindJSON(c, &options); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	if archive.Quiesced() {
		h.SendJSON(c, http.StatusConflict, archive.Status())
		return
	}

	// Uploading a large archive outlives many clients' patience, and a client
	// that gives up must not leave the sandbox stopped with a partial object in
	// the bucket: the export runs to its end and the caller polls /archive/status.
	result, err := archive.Export(context.WithoutCancel(c.Request.Context()), options)
	if errors.Is(err, archive.ErrURLRequired) {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}
	h.SendJSON(c, http.StatusOK, result)
}

// HandleStatus handles GET requests to /archive/status
// @Summary Get the archive status
// @Description Reports whether the sandbox is frozen for an archive export, and which processes were stopped for it.
// @Tags archive
// @Produce json
// @Success 200 {object} QuiesceStatus "Archive status"
// @Router /archive/status [get]
func (h *ArchiveHandler) HandleStatus(c *gin.Context) {
	h.SendJSON(c, http.StatusOK, archive.Status())
}

// HandleResume handles POST requests to /archive/resume
// @Summary Lift the archive freeze
// @Description Makes the API serve every route again after an export. The processes stopped for the export are not relaunched: this exists so a failed or aborted export does not leave the sandbox unusable.
// @Tags archive
// @Produce json
// @Success 200 {object} QuiesceStatus "Archive status"
// @Failure 409 {object} ErrorResponse "An export is in progress"
// @Router /archive/resume [post]
func (h *ArchiveHandler) HandleResume(c *gin.Context) {
	status, err := archive.Resume()
	if err != nil {
		// The export is reading the filesystem: unfreezing now would corrupt the
		// archive it is producing, and it lifts the freeze itself if it fails.
		h.SendError(c, http.StatusConflict, err)
		return
	}
	h.SendJSON(c, http.StatusOK, status)
}
