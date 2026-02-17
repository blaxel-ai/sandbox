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

// AttachDriveRequest represents the request body for attaching a drive
type AttachDriveRequest struct {
	DriveName string `json:"driveName" binding:"required"`
	MountPath string `json:"mountPath" binding:"required"`
	DrivePath string `json:"drivePath"` // Optional, defaults to "/"
}

// AttachDriveResponse represents the response for a successful drive attachment
type AttachDriveResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	DriveName string `json:"driveName"`
	MountPath string `json:"mountPath"`
	DrivePath string `json:"drivePath"`
}

// DetachDriveRequest represents the request body for detaching a drive
type DetachDriveRequest struct {
	MountPath string `json:"mountPath" binding:"required"`
}

// DetachDriveResponse represents the response for a successful drive detachment
type DetachDriveResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	MountPath string `json:"mountPath"`
}

// MountInfo represents information about a mounted drive
type MountInfo struct {
	DriveName string `json:"driveName"`
	MountPath string `json:"mountPath"`
	DrivePath string `json:"drivePath"`
}

// ListMountsResponse represents the response for listing mounted drives
type ListMountsResponse struct {
	Mounts []MountInfo `json:"mounts"`
}

// AttachDrive godoc
// @Summary      Attach a drive to a local path
// @Description  Mounts an agent drive using the blfs binary to a local path, optionally mounting a subpath within the drive
// @Tags         drive
// @Accept       json
// @Produce      json
// @Param        request body AttachDriveRequest true "Drive attachment parameters"
// @Success      200 {object} AttachDriveResponse
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /drives/attach [post]
func (h *DriveHandler) AttachDrive(c *gin.Context) {
	var req AttachDriveRequest
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
	err := drive.MountDrive(req.DriveName, req.MountPath, req.DrivePath)
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

	c.JSON(http.StatusOK, AttachDriveResponse{
		Success:   true,
		Message:   "Drive mounted successfully",
		DriveName: req.DriveName,
		MountPath: req.MountPath,
		DrivePath: req.DrivePath,
	})
}

// DetachDrive godoc
// @Summary      Detach a drive from a local path
// @Description  Unmounts a previously mounted drive from the specified local path
// @Tags         drive
// @Accept       json
// @Produce      json
// @Param        request body DetachDriveRequest true "Drive detachment parameters"
// @Success      200 {object} DetachDriveResponse
// @Failure      400 {object} ErrorResponse
// @Failure      500 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /drives/detach [post]
func (h *DriveHandler) DetachDrive(c *gin.Context) {
	var req DetachDriveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logrus.WithError(err).Error("Failed to bind JSON for drive detachment")
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error: fmt.Sprintf("Invalid request body: %v", err),
		})
		return
	}

	logrus.WithField("mount_path", req.MountPath).Info("Detaching drive")

	// Unmount the drive
	err := drive.UnmountDrive(req.MountPath)
	if err != nil {
		logrus.WithError(err).WithField("mount_path", req.MountPath).Error("Failed to unmount drive")
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: fmt.Sprintf("Failed to unmount drive: %v", err),
		})
		return
	}

	logrus.WithField("mount_path", req.MountPath).Info("Drive detached successfully")

	c.JSON(http.StatusOK, DetachDriveResponse{
		Success:   true,
		Message:   "Drive unmounted successfully",
		MountPath: req.MountPath,
	})
}

// ListMounts godoc
// @Summary      List currently mounted drives
// @Description  Returns a list of all currently mounted drives managed by blfs
// @Tags         drive
// @Produce      json
// @Success      200 {object} ListMountsResponse
// @Failure      500 {object} ErrorResponse
// @Security     BearerAuth
// @Router       /drives/mounts [get]
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
	response := ListMountsResponse{
		Mounts: make([]MountInfo, len(mounts)),
	}
	for i, m := range mounts {
		response.Mounts[i] = MountInfo{
			DriveName: m.DriveName,
			MountPath: m.MountPath,
			DrivePath: m.DrivePath,
		}
	}

	logrus.WithField("mount_count", len(response.Mounts)).Info("Listed mounts successfully")

	c.JSON(http.StatusOK, response)
}

// HealthCheck godoc
// @Summary      Check drive mounting health
// @Description  Returns the health status of the drive mounting service
// @Tags         drive
// @Produce      json
// @Success      200 {object} map[string]interface{}
// @Security     BearerAuth
// @Router       /drives/health [get]
func (h *DriveHandler) HealthCheck(c *gin.Context) {
	// Check if blfs binary is available
	available := drive.CheckBlfsAvailable()
	
	status := "healthy"
	if !available {
		status = "unhealthy"
	}

	c.JSON(http.StatusOK, gin.H{
		"status":         status,
		"blfs_available": available,
		"message":        fmt.Sprintf("Drive mounting service is %s", status),
	})
}
