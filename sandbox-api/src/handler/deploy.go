package handler

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	blaxel "github.com/blaxel-ai/sdk-go"
	"github.com/blaxel-ai/sdk-go/option"
)

// Note: json variable is declared in filesystem.go using jsoniter

// DeployHandler handles deployment operations
type DeployHandler struct {
	*BaseHandler
}

// NewDeployHandler creates a new deploy handler
func NewDeployHandler() *DeployHandler {
	return &DeployHandler{
		BaseHandler: NewBaseHandler(),
	}
}

// AuthMethod represents the authentication method to use
type AuthMethod string // @name AuthMethod

const (
	AuthMethodAPIKey            AuthMethod = "apikey"
	AuthMethodClientCredentials AuthMethod = "client_credentials"
)

// DeployRequest is the request body for deploying an agent
type DeployRequest struct {
	// Authentication
	AuthMethod   AuthMethod `json:"authMethod" example:"apikey"` // "apikey" or "client_credentials"
	APIKey       string     `json:"apiKey,omitempty" example:"bl_xxx"`
	ClientID     string     `json:"clientId,omitempty" example:"client_id"`
	ClientSecret string     `json:"clientSecret,omitempty" example:"client_secret"`
	Workspace    string     `json:"workspace,omitempty" example:"my-workspace"` // Optional, defaults to BL_WORKSPACE env var

	// Deployment configuration
	Name      string                    `json:"name" binding:"required" example:"my-agent"`
	Type      string                    `json:"type" example:"agent"` // agent, function, job, sandbox
	Directory string                    `json:"directory" example:"/app"`
	Runtime   *map[string]interface{}   `json:"runtime,omitempty"`
	Triggers  *[]map[string]interface{} `json:"triggers,omitempty"`
	Policies  []string                  `json:"policies,omitempty"`
	Envs      map[string]string         `json:"envs,omitempty"`
	Public    bool                      `json:"public" example:"false"`
} // @name DeployRequest

// DeployStatusEvent represents a status update during deployment
type DeployStatusEvent struct {
	Type    string      `json:"type"` // "status", "log", "error", "result"
	Status  string      `json:"status,omitempty"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
} // @name DeployStatusEvent

// DeployResult represents the final deployment result
type DeployResult struct {
	Status   string          `json:"status"` // "success", "failed"
	Logs     []string        `json:"logs"`
	Error    string          `json:"error,omitempty"`
	Metadata *DeployMetadata `json:"metadata,omitempty"`
} // @name DeployResult

// DeployMetadata contains deployment metadata
type DeployMetadata struct {
	URL            string `json:"url,omitempty"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Workspace      string `json:"workspace"`
	CallbackSecret string `json:"callbackSecret,omitempty"`
} // @name DeployMetadata

// DeployStreamWriter handles streaming deployment events
type DeployStreamWriter struct {
	gin    *gin.Context
	closed bool
	mu     sync.Mutex
	logs   []string
}

// WriteEvent writes a deployment event to the stream
func (w *DeployStreamWriter) WriteEvent(event DeployStatusEvent) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return fmt.Errorf("writer closed")
	}

	select {
	case <-w.gin.Request.Context().Done():
		w.closed = true
		return fmt.Errorf("client connection closed")
	default:
	}

	// Store logs for final result
	if event.Type == "log" && event.Message != "" {
		w.logs = append(w.logs, event.Message)
	}

	eventJSON, err := json.Marshal(event)
	if err != nil {
		return err
	}
	eventJSON = append(eventJSON, '\n')

	_, err = w.gin.Writer.Write(eventJSON)
	if err != nil {
		w.closed = true
		return err
	}
	w.gin.Writer.Flush()
	return nil
}

// GetLogs returns all collected logs
func (w *DeployStreamWriter) GetLogs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.logs
}

// Close marks the writer as closed
func (w *DeployStreamWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}

// HandleDeploy handles POST requests to /deploy
// @Summary Deploy an application as an agent
// @Description Deploy an application to Blaxel as an agent. Streams status updates and build logs.
// @Tags deploy
// @Accept json
// @Produce application/x-ndjson
// @Param request body DeployRequest true "Deployment request"
// @Success 200 {object} DeployStatusEvent "Stream of deployment events"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 401 {object} ErrorResponse "Authentication failed"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /deploy [post]
func (h *DeployHandler) HandleDeploy(c *gin.Context) {
	var req DeployRequest
	if err := h.BindJSON(c, &req); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Validate authentication
	if req.AuthMethod == "" {
		req.AuthMethod = AuthMethodAPIKey // Default to API key
	}

	// Use BL_WORKSPACE environment variable if workspace not provided
	if req.Workspace == "" {
		req.Workspace = os.Getenv("BL_WORKSPACE")
		if req.Workspace == "" {
			h.SendError(c, http.StatusBadRequest, fmt.Errorf("workspace is required: provide it in the request or set BL_WORKSPACE environment variable"))
			return
		}
	}

	// Set default type
	if req.Type == "" {
		req.Type = "agent"
	}

	// Validate directory
	if req.Directory == "" {
		req.Directory = "."
	}

	// Ensure directory exists
	if _, err := os.Stat(req.Directory); os.IsNotExist(err) {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("directory does not exist: %s", req.Directory))
		return
	}

	// Set headers for streaming JSON events
	c.Writer.Header().Set("Content-Type", "application/x-ndjson")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.Flush()

	// Create stream writer
	sw := &DeployStreamWriter{gin: c}

	// Run deployment
	h.runDeployment(c.Request.Context(), req, sw)
}

// runDeployment executes the deployment process
func (h *DeployHandler) runDeployment(ctx context.Context, req DeployRequest, sw *DeployStreamWriter) {
	logger := logrus.WithFields(logrus.Fields{
		"name":      req.Name,
		"type":      req.Type,
		"workspace": req.Workspace,
		"directory": req.Directory,
		"public":    req.Public,
	})
	logger.Info("Starting deployment")

	// Send initial status
	sw.WriteEvent(DeployStatusEvent{
		Type:    "status",
		Status:  "starting",
		Message: "Initializing deployment...",
	})

	// Create Blaxel client with authentication
	logger.WithField("authMethod", req.AuthMethod).Debug("Creating Blaxel client")
	client, err := h.createBlaxelClient(req)
	if err != nil {
		logger.WithError(err).Error("Failed to create Blaxel client")
		sw.WriteEvent(DeployStatusEvent{
			Type:    "error",
			Message: fmt.Sprintf("Authentication failed: %v", err),
		})
		h.sendFinalResult(sw, "failed", fmt.Sprintf("Authentication failed: %v", err), nil)
		return
	}
	logger.Info("Successfully authenticated with Blaxel")

	sw.WriteEvent(DeployStatusEvent{
		Type:    "status",
		Status:  "authenticated",
		Message: "Successfully authenticated with Blaxel",
	})

	// Create archive
	sw.WriteEvent(DeployStatusEvent{
		Type:    "status",
		Status:  "compressing",
		Message: "Compressing source files...",
	})

	logger.WithField("directory", req.Directory).Debug("Creating archive")
	archivePath, err := h.createArchive(req.Directory, req.Name)
	if err != nil {
		logger.WithError(err).Error("Failed to create archive")
		sw.WriteEvent(DeployStatusEvent{
			Type:    "error",
			Message: fmt.Sprintf("Failed to create archive: %v", err),
		})
		h.sendFinalResult(sw, "failed", fmt.Sprintf("Failed to create archive: %v", err), nil)
		return
	}
	defer os.Remove(archivePath) // Clean up temp file

	// Log archive size
	if archiveInfo, err := os.Stat(archivePath); err == nil {
		logger.WithFields(logrus.Fields{
			"archivePath": archivePath,
			"archiveSize": archiveInfo.Size(),
		}).Info("Archive created successfully")
	}

	sw.WriteEvent(DeployStatusEvent{
		Type:    "log",
		Message: "Archive created successfully",
	})

	// Generate deployment spec
	sw.WriteEvent(DeployStatusEvent{
		Type:    "status",
		Status:  "deploying",
		Message: "Applying deployment configuration...",
	})

	deploySpec := h.generateDeploymentSpec(req)
	if specJSON, err := json.Marshal(deploySpec); err == nil {
		logger.WithField("spec", string(specJSON)).Debug("Generated deployment spec")
	}

	// Apply the deployment
	logger.Debug("Applying deployment to Blaxel")
	uploadURL, callbackSecret, resourceURL, err := h.applyDeployment(ctx, client, req, deploySpec)
	if err != nil {
		logger.WithError(err).Error("Failed to apply deployment")
		sw.WriteEvent(DeployStatusEvent{
			Type:    "error",
			Message: fmt.Sprintf("Failed to apply deployment: %v", err),
		})
		h.sendFinalResult(sw, "failed", fmt.Sprintf("Failed to apply deployment: %v", err), nil)
		return
	}
	logger.WithFields(logrus.Fields{
		"hasUploadURL":      uploadURL != "",
		"hasCallbackSecret": callbackSecret != "",
		"resourceURL":       resourceURL,
	}).Info("Deployment configuration applied")

	sw.WriteEvent(DeployStatusEvent{
		Type:    "log",
		Message: fmt.Sprintf("Deployment configuration applied for %s/%s", req.Type, req.Name),
	})

	// Upload code if we have an upload URL
	if uploadURL != "" {
		sw.WriteEvent(DeployStatusEvent{
			Type:    "status",
			Status:  "uploading",
			Message: "Uploading code to registry...",
		})

		logger.Debug("Uploading archive to registry")
		err = h.uploadArchive(archivePath, uploadURL)
		if err != nil {
			logger.WithError(err).Error("Failed to upload archive")
			sw.WriteEvent(DeployStatusEvent{
				Type:    "error",
				Message: fmt.Sprintf("Failed to upload code: %v", err),
			})
			h.sendFinalResult(sw, "failed", fmt.Sprintf("Failed to upload code: %v", err), nil)
			return
		}
		logger.Info("Archive uploaded successfully")

		sw.WriteEvent(DeployStatusEvent{
			Type:    "log",
			Message: "Code uploaded successfully",
		})
	} else {
		logger.Debug("No upload URL provided, skipping archive upload")
	}

	// Monitor deployment status
	sw.WriteEvent(DeployStatusEvent{
		Type:    "status",
		Status:  "building",
		Message: "Building container image...",
	})

	logger.Info("Starting deployment monitoring")
	err = h.monitorDeployment(ctx, client, req, sw)
	if err != nil {
		logger.WithError(err).Error("Deployment monitoring failed")
		sw.WriteEvent(DeployStatusEvent{
			Type:    "error",
			Message: fmt.Sprintf("Deployment failed: %v", err),
		})
		h.sendFinalResult(sw, "failed", fmt.Sprintf("Deployment failed: %v", err), nil)
		return
	}

	// Deployment successful
	metadata := &DeployMetadata{
		URL:            resourceURL,
		Name:           req.Name,
		Type:           req.Type,
		Workspace:      req.Workspace,
		CallbackSecret: callbackSecret,
	}

	logger.WithField("resourceURL", resourceURL).Info("Deployment completed successfully")

	sw.WriteEvent(DeployStatusEvent{
		Type:    "status",
		Status:  "deployed",
		Message: fmt.Sprintf("Successfully deployed %s/%s", req.Type, req.Name),
	})

	h.sendFinalResult(sw, "success", "", metadata)
}

// createBlaxelClient creates a Blaxel SDK client with the provided authentication
func (h *DeployHandler) createBlaxelClient(req DeployRequest) (blaxel.Client, error) {
	var opts []option.RequestOption

	switch req.AuthMethod {
	case AuthMethodAPIKey:
		if req.APIKey == "" {
			return blaxel.Client{}, fmt.Errorf("API key is required when using apikey authentication")
		}
		opts = append(opts, option.WithAPIKey(req.APIKey))
	case AuthMethodClientCredentials:
		if req.ClientID == "" || req.ClientSecret == "" {
			return blaxel.Client{}, fmt.Errorf("client ID and client secret are required when using client_credentials authentication")
		}
		// Combine client ID and secret in the format expected by the SDK
		clientCredentials := fmt.Sprintf("%s:%s", req.ClientID, req.ClientSecret)
		opts = append(opts, option.WithClientCredentials(clientCredentials))
	default:
		return blaxel.Client{}, fmt.Errorf("invalid authentication method: %s", req.AuthMethod)
	}

	// Set workspace
	opts = append(opts, option.WithWorkspace(req.Workspace))

	client := blaxel.NewClient(opts...)
	return client, nil
}

// createArchive creates a zip archive of the source directory
func (h *DeployHandler) createArchive(directory string, name string) (string, error) {
	// Create temp file for the archive
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("deploy-%s-*.zip", name))
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tmpFile.Close()

	zipWriter := zip.NewWriter(tmpFile)
	defer zipWriter.Close()

	// Get ignore patterns
	ignoredPaths := h.getIgnoredPaths(directory)

	// Walk the directory and add files to the archive
	absDir, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	err = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip ignored paths
		if h.shouldIgnore(path, absDir, ignoredPaths) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(absDir, path)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		// Create header
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		if info.IsDir() {
			header.Name = relPath + "/"
		} else {
			header.Name = relPath
			header.Method = zip.Deflate
		}

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		// Write file content
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = io.Copy(writer, file)
			if err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		os.Remove(tmpFile.Name())
		return "", fmt.Errorf("failed to create archive: %w", err)
	}

	return tmpFile.Name(), nil
}

// getIgnoredPaths returns paths that should be ignored during archiving
func (h *DeployHandler) getIgnoredPaths(directory string) []string {
	// Check for .blaxelignore file
	ignorePath := filepath.Join(directory, ".blaxelignore")
	content, err := os.ReadFile(ignorePath)
	if err != nil {
		// Default ignore patterns
		return []string{
			".blaxel",
			".git",
			"dist",
			".venv",
			"venv",
			"node_modules",
			".env",
			".next",
			"__pycache__",
		}
	}

	// Parse the .blaxelignore file
	lines := strings.Split(string(content), "\n")
	var ignoredPaths []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Handle inline comments
		if idx := strings.Index(line, "#"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
			if line == "" {
				continue
			}
		}
		ignoredPaths = append(ignoredPaths, line)
	}
	return ignoredPaths
}

// shouldIgnore checks if a path should be ignored
func (h *DeployHandler) shouldIgnore(path, basePath string, ignoredPaths []string) bool {
	relPath, err := filepath.Rel(basePath, path)
	if err != nil {
		return false
	}

	for _, ignored := range ignoredPaths {
		if relPath == ignored || strings.HasPrefix(relPath, ignored+"/") {
			return true
		}
		if strings.Contains(relPath, "/"+ignored+"/") {
			return true
		}
	}
	return false
}

// generateDeploymentSpec generates the deployment specification
func (h *DeployHandler) generateDeploymentSpec(req DeployRequest) map[string]interface{} {
	runtime := make(map[string]interface{})
	if req.Runtime != nil {
		runtime = *req.Runtime
	}

	// Add environment variables
	if len(req.Envs) > 0 {
		envs := make([]map[string]string, 0, len(req.Envs))
		for k, v := range req.Envs {
			envs = append(envs, map[string]string{"name": k, "value": v})
		}
		runtime["envs"] = envs
	}

	spec := map[string]interface{}{
		"runtime": runtime,
	}

	if req.Triggers != nil {
		spec["triggers"] = *req.Triggers
	}

	if len(req.Policies) > 0 {
		spec["policies"] = req.Policies
	}

	if req.Public {
		spec["public"] = true
	}

	return map[string]interface{}{
		"apiVersion": "blaxel.ai/v1alpha1",
		"kind":       h.getKind(req.Type),
		"metadata": map[string]interface{}{
			"name": req.Name,
			"labels": map[string]interface{}{
				"x-blaxel-auto-generated": "true",
			},
		},
		"spec": spec,
	}
}

// getKind returns the Kubernetes-style kind for a resource type
func (h *DeployHandler) getKind(resourceType string) string {
	switch resourceType {
	case "function":
		return "Function"
	case "agent":
		return "Agent"
	case "job":
		return "Job"
	case "sandbox":
		return "Sandbox"
	default:
		return "Agent"
	}
}

// applyDeployment applies the deployment to Blaxel
// Returns: uploadURL, callbackSecret, resourceURL, error
func (h *DeployHandler) applyDeployment(ctx context.Context, client blaxel.Client, req DeployRequest, spec map[string]interface{}) (string, string, string, error) {
	logger := logrus.WithFields(logrus.Fields{
		"name":      req.Name,
		"type":      req.Type,
		"workspace": req.Workspace,
	})

	var uploadURL, callbackSecret string
	var httpResponse *http.Response

	opts := []option.RequestOption{
		option.WithResponseInto(&httpResponse),
		option.WithQuery("upload", "true"),
	}

	specJSON, err := json.Marshal(spec)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to marshal spec: %w", err)
	}

	var result interface{}

	switch req.Type {
	case "agent":
		logger.Debug("Creating/updating agent resource")
		var params blaxel.AgentNewParams
		if err := json.Unmarshal(specJSON, &params); err != nil {
			return "", "", "", fmt.Errorf("failed to unmarshal agent params: %w", err)
		}
		result, err = client.Agents.New(ctx, params, opts...)
		if err != nil {
			logger.WithError(err).Debug("Failed to create agent, trying update")
			// Try update if create fails
			var updateParams blaxel.AgentUpdateParams
			if err2 := json.Unmarshal(specJSON, &updateParams); err2 != nil {
				return "", "", "", fmt.Errorf("failed to create agent: %w", err)
			}
			result, err = client.Agents.Update(ctx, req.Name, updateParams, opts...)
			if err != nil {
				return "", "", "", fmt.Errorf("failed to create/update agent: %w", err)
			}
			logger.Info("Agent updated successfully")
		} else {
			logger.Info("Agent created successfully")
		}
	case "function":
		logger.Debug("Creating/updating function resource")
		var params blaxel.FunctionNewParams
		if err := json.Unmarshal(specJSON, &params); err != nil {
			return "", "", "", fmt.Errorf("failed to unmarshal function params: %w", err)
		}
		result, err = client.Functions.New(ctx, params, opts...)
		if err != nil {
			logger.WithError(err).Debug("Failed to create function, trying update")
			var updateParams blaxel.FunctionUpdateParams
			if err2 := json.Unmarshal(specJSON, &updateParams); err2 != nil {
				return "", "", "", fmt.Errorf("failed to create function: %w", err)
			}
			result, err = client.Functions.Update(ctx, req.Name, updateParams, opts...)
			if err != nil {
				return "", "", "", fmt.Errorf("failed to create/update function: %w", err)
			}
			logger.Info("Function updated successfully")
		} else {
			logger.Info("Function created successfully")
		}
	case "job":
		logger.Debug("Creating/updating job resource")
		var params blaxel.JobNewParams
		if err := json.Unmarshal(specJSON, &params); err != nil {
			return "", "", "", fmt.Errorf("failed to unmarshal job params: %w", err)
		}
		result, err = client.Jobs.New(ctx, params, opts...)
		if err != nil {
			logger.WithError(err).Debug("Failed to create job, trying update")
			var updateParams blaxel.JobUpdateParams
			if err2 := json.Unmarshal(specJSON, &updateParams); err2 != nil {
				return "", "", "", fmt.Errorf("failed to create job: %w", err)
			}
			result, err = client.Jobs.Update(ctx, req.Name, updateParams, opts...)
			if err != nil {
				return "", "", "", fmt.Errorf("failed to create/update job: %w", err)
			}
			logger.Info("Job updated successfully")
		} else {
			logger.Info("Job created successfully")
		}
	case "sandbox":
		logger.Debug("Creating/updating sandbox resource")
		var params blaxel.SandboxNewParams
		if err := json.Unmarshal(specJSON, &params); err != nil {
			return "", "", "", fmt.Errorf("failed to unmarshal sandbox params: %w", err)
		}
		result, err = client.Sandboxes.New(ctx, params, opts...)
		if err != nil {
			logger.WithError(err).Debug("Failed to create sandbox, trying update")
			var updateParams blaxel.SandboxUpdateParams
			if err2 := json.Unmarshal(specJSON, &updateParams); err2 != nil {
				return "", "", "", fmt.Errorf("failed to create sandbox: %w", err)
			}
			result, err = client.Sandboxes.Update(ctx, req.Name, updateParams, opts...)
			if err != nil {
				return "", "", "", fmt.Errorf("failed to create/update sandbox: %w", err)
			}
			logger.Info("Sandbox updated successfully")
		} else {
			logger.Info("Sandbox created successfully")
		}
	default:
		return "", "", "", fmt.Errorf("unsupported resource type: %s", req.Type)
	}

	// Extract upload URL from response header
	if httpResponse != nil {
		uploadURL = httpResponse.Header.Get("X-Blaxel-Upload-Url")
		logger.WithFields(logrus.Fields{
			"statusCode":   httpResponse.StatusCode,
			"hasUploadURL": uploadURL != "",
		}).Debug("Received API response")
	}

	// Extract callback secret and resource URL from response
	callbackSecret = h.extractCallbackSecret(result)
	resourceURL := h.extractResourceURL(result)

	// Log the result for debugging
	if resultJSON, err := json.Marshal(result); err == nil {
		logger.WithField("response", string(resultJSON)).Debug("API response body")
	}

	return uploadURL, callbackSecret, resourceURL, nil
}

// extractCallbackSecret extracts the callback secret from an API response
func (h *DeployHandler) extractCallbackSecret(response interface{}) string {
	jsonData, err := json.Marshal(response)
	if err != nil {
		return ""
	}

	var resource map[string]interface{}
	if err := json.Unmarshal(jsonData, &resource); err != nil {
		return ""
	}

	// Navigate through the JSON structure to find callback secret
	if spec, ok := resource["spec"].(map[string]interface{}); ok {
		if triggers, ok := spec["triggers"].([]interface{}); ok {
			for _, trigger := range triggers {
				if triggerMap, ok := trigger.(map[string]interface{}); ok {
					if config, ok := triggerMap["configuration"].(map[string]interface{}); ok {
						if callbackSecret, ok := config["callbackSecret"].(string); ok && callbackSecret != "" {
							if !strings.Contains(callbackSecret, "*") {
								return callbackSecret
							}
						}
					}
				}
			}
		}
	}

	return ""
}

// extractResourceURL extracts the resource URL from an API response
func (h *DeployHandler) extractResourceURL(response interface{}) string {
	jsonData, err := json.Marshal(response)
	if err != nil {
		return ""
	}

	var resource map[string]interface{}
	if err := json.Unmarshal(jsonData, &resource); err != nil {
		return ""
	}

	// Extract URL from metadata.url
	if metadata, ok := resource["metadata"].(map[string]interface{}); ok {
		if url, ok := metadata["url"].(string); ok {
			return url
		}
	}

	return ""
}

// uploadArchive uploads the archive to the provided URL
func (h *DeployHandler) uploadArchive(archivePath, uploadURL string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	req, err := http.NewRequest("PUT", uploadURL, file)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.ContentLength = fileInfo.Size()
	req.Header.Set("Content-Type", "application/zip")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// monitorDeployment monitors the deployment status and streams build logs
func (h *DeployHandler) monitorDeployment(ctx context.Context, client blaxel.Client, req DeployRequest, sw *DeployStreamWriter) error {
	logger := logrus.WithFields(logrus.Fields{
		"name":      req.Name,
		"type":      req.Type,
		"workspace": req.Workspace,
	})

	// Wait a bit for the build to start
	logger.Debug("Waiting for build to start")
	time.Sleep(2 * time.Second)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	timeout := time.After(15 * time.Minute)
	startTime := time.Now().UTC()
	seenLogs := make(map[string]bool)
	lastStatus := ""
	pollCount := 0

	for {
		select {
		case <-ctx.Done():
			logger.Warn("Deployment cancelled by context")
			return fmt.Errorf("deployment cancelled")
		case <-timeout:
			logger.Error("Deployment timed out after 15 minutes")
			return fmt.Errorf("deployment timed out after 15 minutes")
		case <-ticker.C:
			pollCount++
			// Get resource status
			status, err := h.getResourceStatus(ctx, client, req.Type, req.Name)
			if err != nil {
				logger.WithError(err).Debug("Failed to get resource status")
				continue
			}

			// Log status changes
			if status != lastStatus {
				logger.WithFields(logrus.Fields{
					"previousStatus": lastStatus,
					"newStatus":      status,
					"pollCount":      pollCount,
				}).Info("Resource status changed")
				lastStatus = status
			}

			// Fetch and stream build logs
			logs, err := h.fetchBuildLogs(ctx, client, req, startTime, seenLogs)
			if err != nil {
				logger.WithError(err).Debug("Failed to fetch build logs")
			} else if len(logs) > 0 {
				logger.WithField("newLogsCount", len(logs)).Debug("Fetched new build logs")
				for _, log := range logs {
					sw.WriteEvent(DeployStatusEvent{
						Type:    "log",
						Message: log,
					})
				}
			}

			// Check status
			switch status {
			case "BUILDING":
				sw.WriteEvent(DeployStatusEvent{
					Type:    "status",
					Status:  "building",
					Message: "Building container image...",
				})
			case "DEPLOYING":
				sw.WriteEvent(DeployStatusEvent{
					Type:    "status",
					Status:  "deploying",
					Message: "Deploying to cluster...",
				})
			case "DEPLOYED":
				logger.WithField("pollCount", pollCount).Info("Resource deployed successfully")
				return nil
			case "FAILED":
				logger.WithField("pollCount", pollCount).Error("Resource deployment failed")
				return fmt.Errorf("deployment failed")
			case "DEACTIVATED", "DEACTIVATING", "DELETING":
				logger.WithField("status", status).Warn("Resource is being deactivated or deleted")
				return fmt.Errorf("resource is being deactivated or deleted")
			}
		}
	}
}

// getResourceStatus gets the current status of a resource
func (h *DeployHandler) getResourceStatus(ctx context.Context, client blaxel.Client, resourceType, name string) (string, error) {
	logger := logrus.WithFields(logrus.Fields{
		"name": name,
		"type": resourceType,
	})

	var result interface{}
	var err error

	switch resourceType {
	case "agent":
		result, err = client.Agents.Get(ctx, name, blaxel.AgentGetParams{})
	case "function":
		result, err = client.Functions.Get(ctx, name, blaxel.FunctionGetParams{})
	case "job":
		result, err = client.Jobs.Get(ctx, name, blaxel.JobGetParams{})
	case "sandbox":
		result, err = client.Sandboxes.Get(ctx, name, blaxel.SandboxGetParams{})
	default:
		return "", fmt.Errorf("unknown resource type: %s", resourceType)
	}

	if err != nil {
		logger.WithError(err).Debug("Failed to get resource")
		return "", err
	}

	// Convert result to map
	jsonData, err := json.Marshal(result)
	if err != nil {
		return "", err
	}

	var resource map[string]interface{}
	if err := json.Unmarshal(jsonData, &resource); err != nil {
		return "", err
	}

	// Extract status from the resource
	if status, ok := resource["status"].(string); ok {
		logger.WithField("status", status).Trace("Got resource status")
		return status, nil
	}

	logger.WithField("response", string(jsonData)).Debug("Could not extract status from response")
	return "UNKNOWN", nil
}

// fetchBuildLogs fetches build logs from the observability API
func (h *DeployHandler) fetchBuildLogs(ctx context.Context, client blaxel.Client, req DeployRequest, startTime time.Time, seenLogs map[string]bool) ([]string, error) {
	start := startTime.Format("2006-01-02T15:04:05")
	end := startTime.Add(15 * time.Minute).Format("2006-01-02T15:04:05")

	queryOpts := []option.RequestOption{
		option.WithQuery("start", start),
		option.WithQuery("end", end),
		option.WithQuery("resourceType", h.pluralizeResourceType(req.Type)),
		option.WithQuery("workloadIds", req.Name),
		option.WithQuery("type", "all"),
		option.WithQuery("limit", "1000"),
		option.WithQuery("severity", "all,UNKNOWN,TRACE,DEBUG,INFO,WARNING,ERROR,FATAL"),
		option.WithQuery("workspace", req.Workspace),
	}

	var response map[string]struct {
		Logs []struct {
			Timestamp string `json:"timestamp"`
			Message   string `json:"message"`
		} `json:"logs"`
	}

	err := client.Get(ctx, "/observability/logs", nil, &response, queryOpts...)
	if err != nil {
		return nil, err
	}

	var messages []string
	if resourceData, ok := response[req.Name]; ok {
		for _, log := range resourceData.Logs {
			key := fmt.Sprintf("%s:%s", log.Timestamp, log.Message)
			if !seenLogs[key] {
				seenLogs[key] = true
				messages = append(messages, log.Message)
			}
		}
	}

	return messages, nil
}

// pluralizeResourceType converts singular resource type to plural
func (h *DeployHandler) pluralizeResourceType(resourceType string) string {
	switch resourceType {
	case "agent":
		return "agents"
	case "function":
		return "functions"
	case "job":
		return "jobs"
	case "sandbox":
		return "sandboxes"
	default:
		return resourceType + "s"
	}
}

// sendFinalResult sends the final deployment result
func (h *DeployHandler) sendFinalResult(sw *DeployStreamWriter, status, errorMsg string, metadata *DeployMetadata) {
	result := DeployResult{
		Status:   status,
		Logs:     sw.GetLogs(),
		Error:    errorMsg,
		Metadata: metadata,
	}

	sw.WriteEvent(DeployStatusEvent{
		Type: "result",
		Data: result,
	})
}
