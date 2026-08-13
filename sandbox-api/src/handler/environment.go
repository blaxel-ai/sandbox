package handler

import (
	"net/http"
	"os"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// defaultMetadataPath is the guest metadata document served by the host
// (vmm-manager) through the initrd's FUSE mount. It always reflects the
// current generation: on an environment update the host persists the new set,
// the initrd refetches the document, then pings POST /environment/reload so
// this process adopts it. Absent on VMs booted from an initrd that predates
// the metadata protocol. Overridable through BL_METADATA_PATH.
const defaultMetadataPath = "/bl/metadata"

func metadataPath() string {
	if path := os.Getenv("BL_METADATA_PATH"); path != "" {
		return path
	}
	return defaultMetadataPath
}

// metadataDocument is the subset of the guest metadata document this handler
// reads. The environment carried is the host's complete set, so a variable the
// host no longer has must be unset here too.
type metadataDocument struct {
	Generation  int64             `json:"generation"`
	Environment map[string]string `json:"environment"`
}

// EnvironmentHandler applies environment updates to the sandbox-api process
// itself. A live process's environment cannot be changed from outside, so the
// initrd notifies this endpoint after applying a new metadata generation; the
// values are set with os.Setenv, which also makes every process spawned
// afterwards (process API, terminals, restarts) inherit them.
type EnvironmentHandler struct {
	*BaseHandler

	mu sync.Mutex
	// Keys applied from the metadata document, so a key the next generation no
	// longer carries is unset rather than left behind. Keys that were never in
	// the document (the image ENV, boot-time variables) are never touched.
	applied map[string]struct{}
}

// NewEnvironmentHandler creates a new environment handler.
func NewEnvironmentHandler() *EnvironmentHandler {
	return &EnvironmentHandler{
		BaseHandler: NewBaseHandler(),
		applied:     map[string]struct{}{},
	}
}

// ReloadResponse is the response body for the environment reload endpoint.
type ReloadResponse struct {
	Generation int64 `json:"generation"`
	Applied    int   `json:"applied"`
	Removed    int   `json:"removed"`
}

// HandleReload reloads the environment from the guest metadata document
// @Summary Reload environment from guest metadata
// @Description Re-reads /bl/metadata and applies its environment to the sandbox-api process, so this process and every process started afterwards see the current values. Called by the guest init after an environment update; safe to call manually.
// @Tags system
// @Produce json
// @Success 200 {object} ReloadResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /environment/reload [post]
func (h *EnvironmentHandler) HandleReload(c *gin.Context) {
	raw, err := os.ReadFile(metadataPath())
	if err != nil {
		if os.IsNotExist(err) {
			h.SendError(c, http.StatusNotFound, err)
			return
		}
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	var doc metadataDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	removed := 0
	for key := range h.applied {
		if _, ok := doc.Environment[key]; !ok {
			if err := os.Unsetenv(key); err == nil {
				removed++
			}
			delete(h.applied, key)
		}
	}
	applied := 0
	for key, value := range doc.Environment {
		if err := os.Setenv(key, value); err != nil {
			logrus.WithError(err).WithField("key", key).Warn("Failed to set environment variable")
			continue
		}
		h.applied[key] = struct{}{}
		applied++
	}

	logrus.WithFields(logrus.Fields{
		"generation": doc.Generation,
		"applied":    applied,
		"removed":    removed,
	}).Info("Environment reloaded from guest metadata")

	c.JSON(http.StatusOK, ReloadResponse{
		Generation: doc.Generation,
		Applied:    applied,
		Removed:    removed,
	})
}
