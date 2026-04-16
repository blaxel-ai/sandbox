package handler

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/blaxel-ai/sandbox-api/src/handler/drive"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

type DriveHandler struct {
	BaseHandler
}

func NewDriveHandler() *DriveHandler {
	return &DriveHandler{
		BaseHandler: BaseHandler{},
	}
}

// DriveMountRequest represents the request body for mounting a drive
type DriveMountRequest struct {
	DriveName string `json:"driveName" binding:"required"`
	MountPath string `json:"mountPath" binding:"required"`
	DrivePath string `json:"drivePath"` // Optional, defaults to "/"
	ReadOnly  bool   `json:"readOnly"`  // Optional, defaults to false
} // @name DriveMountRequest

// DriveMountResponse represents the response for a successful drive mount
type DriveMountResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	DriveName string `json:"driveName"`
	MountPath string `json:"mountPath"`
	DrivePath string `json:"drivePath"`
	ReadOnly  bool   `json:"readOnly"`
} // @name DriveMountResponse

// DriveUnmountResponse represents the response for a successful drive unmount
type DriveUnmountResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	MountPath string `json:"mountPath"`
} // @name DriveUnmountResponse

// DriveMountInfo represents information about a mounted drive
type DriveMountInfo struct {
	DriveName string `json:"driveName"`
	MountPath string `json:"mountPath"`
	DrivePath string `json:"drivePath"`
	ReadOnly  bool   `json:"readOnly"`
} // @name DriveMountInfo

// DriveListResponse represents the response for listing mounted drives
type DriveListResponse struct {
	Mounts []DriveMountInfo `json:"mounts"`
} // @name DriveListResponse

// AttachDrive godoc
// @Summary      Attach a drive to a local path
// @Description  Mounts an agent drive using the blfs binary to a local path, optionally mounting a subpath within the drive
// @Tags         drive
// @Accept       json
// @Produce      json
// @Param        request body DriveMountRequest true "Drive attachment parameters"
// @Success      200 {object} DriveMountResponse
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /drives/mount [post]
func (h *DriveHandler) AttachDrive(c *gin.Context) {
	var req DriveMountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logrus.WithError(err).Error("Failed to bind JSON for drive attachment")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	// Default drive path to "/" if not specified
	if req.DrivePath == "" {
		req.DrivePath = "/"
	}

	req.MountPath = drive.NormalizeMountPath(req.MountPath)

	// Validate drive name and mount path (security: path traversal and injection)
	if err := drive.ValidateDriveName(req.DriveName); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: err.Error(),
		})
		return
	}
	if err := drive.ValidateMountPath(req.MountPath); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: err.Error(),
		})
		return
	}

	// Validate drive path starts with /
	if !strings.HasPrefix(req.DrivePath, "/") {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid drive path: Drive path must start with /",
		})
		return
	}

	logrus.WithFields(logrus.Fields{
		"drive_name": req.DriveName,
		"mount_path": req.MountPath,
		"drive_path": req.DrivePath,
	}).Info("Attaching drive")

	// Mount the drive
	err := drive.MountDrive(req.DriveName, req.MountPath, req.DrivePath, req.ReadOnly)
	if err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"drive_name": req.DriveName,
			"mount_path": req.MountPath,
			"drive_path": req.DrivePath,
		}).Error("Failed to mount drive")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: fmt.Sprintf("Failed to mount drive: %v", err),
		})
		return
	}

	logrus.WithFields(logrus.Fields{
		"drive_name": req.DriveName,
		"mount_path": req.MountPath,
		"drive_path": req.DrivePath,
	}).Info("Drive attached successfully")

	c.JSON(http.StatusOK, DriveMountResponse{
		Success:   true,
		Message:   "Drive mounted successfully",
		DriveName: req.DriveName,
		MountPath: req.MountPath,
		DrivePath: req.DrivePath,
		ReadOnly:  req.ReadOnly,
	})
}

// DetachDrive godoc
// @Summary      Detach a drive from a local path
// @Description  Unmounts a previously mounted drive from the specified local path
// @Tags         drive
// @Produce      json
// @Param        mountPath path string true "Mount path to detach (must start with /, e.g. /mnt/test)"
// @Success      200 {object} DriveUnmountResponse
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /drives/mount/{mountPath} [delete]
func (h *DriveHandler) DetachDrive(c *gin.Context) {
	// Get mount path from context (set by middleware)
	mountPathRaw, exists := c.Get("mountPath")
	if !exists {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Mount path not provided",
		})
		return
	}

	mountPath := mountPathRaw.(string)

	logrus.WithField("mount_path", mountPath).Info("Detaching drive")

	// Unmount the drive
	err := drive.UnmountDrive(mountPath)
	if err != nil {
		logrus.WithError(err).WithField("mount_path", mountPath).Error("Failed to unmount drive")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: fmt.Sprintf("Failed to unmount drive: %v", err),
		})
		return
	}

	logrus.WithField("mount_path", mountPath).Info("Drive detached successfully")

	c.JSON(http.StatusOK, DriveUnmountResponse{
		Success:   true,
		Message:   "Drive unmounted successfully",
		MountPath: mountPath,
	})
}

// ListMounts godoc
// @Summary      List currently mounted drives
// @Description  Returns a list of all currently mounted drives managed by blfs
// @Tags         drive
// @Produce      json
// @Success      200 {object} DriveListResponse
// @Failure      500 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /drives/mount [get]
func (h *DriveHandler) ListMounts(c *gin.Context) {
	logrus.Info("Listing mounted drives")

	mounts, err := drive.ListMounts()
	if err != nil {
		logrus.WithError(err).Error("Failed to list mounts")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: fmt.Sprintf("Failed to list mounts: %v", err),
		})
		return
	}

	// Convert internal mount info to API response format
	response := DriveListResponse{
		Mounts: make([]DriveMountInfo, len(mounts)),
	}
	for i, m := range mounts {
		response.Mounts[i] = DriveMountInfo{
			DriveName: m.DriveName,
			MountPath: m.MountPath,
			DrivePath: m.DrivePath,
			ReadOnly:  m.ReadOnly,
		}
	}

	logrus.WithField("mount_count", len(response.Mounts)).Info("Listed mounts successfully")

	c.JSON(http.StatusOK, response)
}

