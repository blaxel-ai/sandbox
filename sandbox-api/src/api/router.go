package api

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "github.com/blaxel-ai/sandbox-api/docs" // Import generated docs
	"github.com/blaxel-ai/sandbox-api/src/handler"
)

// SetupRouter configures all the routes for the Sandbox API
// If disableRequestLogging is true, the logrus middleware will be skipped
// If enableProcessingTime is true, the Server-Timing header middleware will be added
func SetupRouter(disableRequestLogging bool, enableProcessingTime bool) *gin.Engine {
	// Initialize the router
	r := gin.New()

	// Add recovery middleware
	r.Use(gin.Recovery())

	// Add middleware for CORS
	r.Use(corsMiddleware())

	// Add middleware to prevent caching
	r.Use(noCacheMiddleware())

	// Add processing time middleware if enabled
	if enableProcessingTime {
		r.Use(processingTimeMiddleware())
	}

	// Add logrus middleware unless disabled
	if !disableRequestLogging {
		r.Use(logrusMiddleware())
	}

	// Swagger documentation route
	r.GET("/swagger", func(c *gin.Context) {
		c.Redirect(301, "/swagger/index.html")
	})
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// Initialize handlers
	baseHandler := handler.NewBaseHandler()
	fsHandler := handler.NewFileSystemHandler()
	processHandler := handler.NewProcessHandler()
	networkHandler := handler.NewNetworkHandler()
	codegenHandler := handler.NewCodegenHandler(fsHandler)
	systemHandler := handler.NewSystemHandler()
	driveHandler := handler.NewDriveHandler()

	// Check if terminal is disabled via environment variable
	disableTerminal := os.Getenv("DISABLE_TERMINAL") == "true" || os.Getenv("DISABLE_TERMINAL") == "1"

	// Custom filesystem tree router middleware to handle tree-specific routes
	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		method := c.Request.Method

		// Check if this is a tree request
		if strings.HasPrefix(path, "/filesystem/tree") {
			// Extract the path after "/filesystem/tree"
			trimmedPath := strings.TrimPrefix(path, "/filesystem/tree")

			// Handle the trimmed path - if it's empty, we're referring to the root
			if trimmedPath == "" {
				trimmedPath = "/"
			}

			// Clean the path to avoid issues with extra slashes
			// We're not using filepath.Clean because it might change the path differently on Windows
			// Instead, just ensure there's one leading slash and no double slashes
			if trimmedPath != "/" {
				// Ensure it starts with a slash
				if !strings.HasPrefix(trimmedPath, "/") {
					trimmedPath = "/" + trimmedPath
				}

				// Replace any double slashes with single ones
				for strings.Contains(trimmedPath, "//") {
					trimmedPath = strings.ReplaceAll(trimmedPath, "//", "/")
				}
			}

			// Set the root path value in the context
			c.Set("rootPath", trimmedPath)

			// Handle based on method
			switch method {
			case "GET":
				fsHandler.HandleGetTree(c)
				c.Abort()
				return
			case "PUT":
				fsHandler.HandleCreateOrUpdateTree(c)
				c.Abort()
				return
			case "DELETE":
				fsHandler.HandleDeleteTree(c)
				c.Abort()
				return
			}
		}
		c.Next()
	})

	// HEAD handler for checking endpoint existence
	head := headHandler()

	// Multipart upload routes (separate endpoint to avoid wildcard conflicts)
	r.GET("/filesystem-multipart", fsHandler.HandleListMultipartUploads)
	r.HEAD("/filesystem-multipart", head)
	r.POST("/filesystem-multipart/initiate/*path", fsHandler.HandleInitiateMultipartUpload)
	r.PUT("/filesystem-multipart/:uploadId/part", fsHandler.HandleUploadPart)
	r.POST("/filesystem-multipart/:uploadId/complete", fsHandler.HandleCompleteMultipartUpload)
	r.DELETE("/filesystem-multipart/:uploadId/abort", fsHandler.HandleAbortMultipartUpload)
	r.GET("/filesystem-multipart/:uploadId/parts", fsHandler.HandleListParts)
	r.HEAD("/filesystem-multipart/:uploadId/parts", head)

	// Filesystem routes
	r.GET("/filesystem-find/*path", fsHandler.HandleFind)
	r.HEAD("/filesystem-find/*path", head)
	r.GET("/filesystem-search/*path", fsHandler.HandleFuzzySearch)
	r.HEAD("/filesystem-search/*path", head)
	r.GET("/filesystem-content-search/*path", fsHandler.HandleContentSearch)
	r.HEAD("/filesystem-content-search/*path", head)
	r.GET("/watch/filesystem/*path", fsHandler.HandleWatchDirectory)
	r.HEAD("/watch/filesystem/*path", head)
	r.GET("/filesystem/*path", fsHandler.HandleGetFile)
	r.HEAD("/filesystem/*path", head)
	r.PUT("/filesystem/*path", fsHandler.HandleCreateOrUpdateFile)
	r.DELETE("/filesystem/*path", fsHandler.HandleDeleteFile)

	// Process routes
	r.GET("/process", processHandler.HandleListProcesses)
	r.HEAD("/process", head)
	r.POST("/process", processHandler.HandleExecuteCommand)
	r.GET("/process/:identifier/logs", processHandler.HandleGetProcessLogs)
	r.HEAD("/process/:identifier/logs", head)
	r.GET("/process/:identifier/logs/stream", processHandler.HandleGetProcessLogsStream)
	r.HEAD("/process/:identifier/logs/stream", head)
	r.DELETE("/process/:identifier", processHandler.HandleStopProcess)
	r.DELETE("/process/:identifier/kill", processHandler.HandleKillProcess)
	r.GET("/process/:identifier", processHandler.HandleGetProcess)
	r.HEAD("/process/:identifier", head)

	// Network routes
	r.GET("/network/process/:pid/ports", networkHandler.HandleGetPorts)
	r.HEAD("/network/process/:pid/ports", head)
	r.POST("/network/process/:pid/monitor", networkHandler.HandleMonitorPorts)
	r.DELETE("/network/process/:pid/monitor", networkHandler.HandleStopMonitoringPorts)

	// Codegen routes
	r.PUT("/codegen/fastapply/*path", codegenHandler.HandleFastApply)
	r.GET("/codegen/reranking/*path", codegenHandler.HandleReranking)
	r.HEAD("/codegen/reranking/*path", head)

	// Terminal routes (web-based terminal with PTY)
	// Can be disabled with DISABLE_TERMINAL=true environment variable
	if !disableTerminal {
		terminalHandler := handler.NewTerminalHandler()
		r.GET("/terminal", terminalHandler.HandleTerminalPage)
		r.HEAD("/terminal", head)
		r.GET("/terminal/ws", terminalHandler.HandleTerminalWS)
		r.HEAD("/terminal/ws", head)
	} else {
		logrus.Info("Terminal endpoint disabled via DISABLE_TERMINAL environment variable")
	}

	// System routes
	r.POST("/upgrade", systemHandler.HandleUpgrade)
	r.HEAD("/upgrade", head)
	r.GET("/health", systemHandler.HandleHealth)
	r.HEAD("/health", head)

	// Drive routes (for mounting/unmounting agent drives)
	// REST API convention:
	// - GET /drives -> list all mounted drives
	// - POST /drives -> attach/mount a drive
	// - DELETE /drives/{mountPath} -> detach/unmount a drive
	r.GET("/drives", driveHandler.ListMounts)
	r.HEAD("/drives", head)
	r.POST("/drives", driveHandler.AttachDrive)
	
	// Custom middleware to handle DELETE /drives/*mountPath
	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		method := c.Request.Method

		// Check if this is a DELETE request to /drives/*
		if method == "DELETE" && strings.HasPrefix(path, "/drives/") {
			// Extract the mount path after "/drives/"
			mountPath := strings.TrimPrefix(path, "/drives")
			
			// Ensure mountPath starts with /
			if !strings.HasPrefix(mountPath, "/") {
				mountPath = "/" + mountPath
			}
			
			// Set the mount path in the context
			c.Set("mountPath", mountPath)
			
			// Call the detach handler
			driveHandler.DetachDrive(c)
			c.Abort()
			return
		}
		c.Next()
	})

	// Root welcome endpoint - handles all HTTP methods
	r.GET("/", baseHandler.HandleWelcome)
	r.POST("/", baseHandler.HandleWelcome)
	r.PUT("/", baseHandler.HandleWelcome)
	r.DELETE("/", baseHandler.HandleWelcome)
	r.PATCH("/", baseHandler.HandleWelcome)
	r.OPTIONS("/", baseHandler.HandleWelcome)

	return r
}

// corsMiddleware adds CORS headers to all responses
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, HEAD, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}

// headHandler returns a simple 200 OK for HEAD requests to check endpoint existence
func headHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Status(http.StatusOK)
	}
}

// noCacheMiddleware adds no-cache headers to all responses to prevent caching issues
func noCacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Writer.Header().Set("Pragma", "no-cache")
		c.Writer.Header().Set("Expires", "0")
		c.Writer.Header().Set("X-Content-Type-Options", "nosniff")

		c.Next()
	}
}

// sensitiveQueryParams contains query parameter names that should be redacted from logs
var sensitiveQueryParams = []string{
	"api_key", "apikey", "api-key",
	"token", "access_token", "refresh_token", "auth_token", "bearer",
	"password", "passwd", "pwd",
	"secret", "client_secret", "api_secret",
	"key", "private_key", "encryption_key",
	"authorization", "auth",
	"credential", "credentials",
	"session", "session_id", "sessionid",
	"jwt",
}

// redactSecrets redacts sensitive information from a URL path with query string
func redactSecrets(pathWithQuery string) string {
	// Split path and query
	parts := strings.SplitN(pathWithQuery, "?", 2)
	if len(parts) != 2 {
		return pathWithQuery // No query string, return as-is
	}

	basePath := parts[0]
	queryString := parts[1]

	// Parse query parameters
	values, err := url.ParseQuery(queryString)
	if err != nil {
		// If parsing fails, try to redact using pattern matching
		return redactQueryPatterns(pathWithQuery)
	}

	// Check if any sensitive param exists
	hasSecrets := false
	for _, param := range sensitiveQueryParams {
		if values.Get(param) != "" {
			hasSecrets = true
			break
		}
		// Also check case-insensitive
		for key := range values {
			if strings.EqualFold(key, param) {
				hasSecrets = true
				break
			}
		}
	}

	if !hasSecrets {
		return pathWithQuery
	}

	// Redact sensitive values
	for key := range values {
		for _, param := range sensitiveQueryParams {
			if strings.EqualFold(key, param) {
				values.Set(key, "[REDACTED]")
				break
			}
		}
	}

	return basePath + "?" + values.Encode()
}

// redactQueryPatterns redacts secrets using regex patterns when URL parsing fails
func redactQueryPatterns(pathWithQuery string) string {
	result := pathWithQuery
	for _, param := range sensitiveQueryParams {
		// Match param=value patterns (case-insensitive)
		pattern := regexp.MustCompile(`(?i)(` + regexp.QuoteMeta(param) + `=)[^&\s]*`)
		result = pattern.ReplaceAllString(result, "${1}[REDACTED]")
	}
	return result
}

func logrusMiddleware() gin.HandlerFunc {
	var skip map[string]struct{}

	return func(c *gin.Context) {

		// other handler can change c.Path so:
		path := c.Request.URL.Path
		if c.Request.URL.RawQuery != "" {
			path = path + "?" + c.Request.URL.RawQuery
		}

		// Redact secrets from the path before logging
		sanitizedPath := redactSecrets(path)

		start := time.Now()
		c.Next()
		stop := time.Since(start)
		latency := int(math.Ceil(float64(stop.Nanoseconds()) / 1000000.0))
		statusCode := c.Writer.Status()
		dataLength := c.Writer.Size()
		if dataLength < 0 {
			dataLength = 0
		}

		if _, ok := skip[path]; ok {
			return
		}

		if len(c.Errors) > 0 {
			logrus.Error(c.Errors.ByType(gin.ErrorTypePrivate).String())
		} else {
			msg := fmt.Sprintf("%s %s %d %d %dms", c.Request.Method, sanitizedPath, statusCode, dataLength, latency)
			if statusCode >= http.StatusInternalServerError {
				logrus.Error(msg)
			} else if statusCode >= http.StatusBadRequest {
				logrus.Error(msg)
			} else {
				logrus.Info(msg)
			}
		}
	}
}
