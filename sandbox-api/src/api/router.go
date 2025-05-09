package api

import (
	"strings"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "github.com/blaxel-ai/sandbox-api/docs" // Import generated docs
	"github.com/blaxel-ai/sandbox-api/src/handler"
)

// SetupRouter configures all the routes for the Sandbox API
func SetupRouter() *gin.Engine {
	// Initialize the router
	r := gin.Default()

	// Add middleware for CORS
	r.Use(corsMiddleware())

	// Swagger documentation route
	r.GET("/swagger", func(c *gin.Context) {
		c.Redirect(301, "/swagger/index.html")
	})
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// Initialize handlers
	fsHandler := handler.NewFileSystemHandler()
	processHandler := handler.NewProcessHandler()
	networkHandler := handler.NewNetworkHandler()

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
			if method == "GET" {
				fsHandler.HandleGetTree(c)
				c.Abort()
				return
			} else if method == "PUT" {
				fsHandler.HandleCreateOrUpdateTree(c)
				c.Abort()
				return
			}
		}

		c.Next()
	})

	// Filesystem routes
	r.GET("/watch/filesystem/*path", fsHandler.HandleWatchDirectory)
	r.GET("/filesystem/*path", fsHandler.HandleGetFile)
	r.PUT("/filesystem/*path", fsHandler.HandleCreateOrUpdateFile)
	r.DELETE("/filesystem/*path", fsHandler.HandleDeleteFile)

	// Process routes
	r.GET("/process", processHandler.HandleListProcesses)
	r.POST("/process", processHandler.HandleExecuteCommand)
	r.GET("/process/:identifier/logs", processHandler.HandleGetProcessLogs)
	r.GET("/process/:identifier/logs/stream", processHandler.HandleGetProcessLogsStream)
	r.DELETE("/process/:identifier", processHandler.HandleStopProcess)
	r.DELETE("/process/:identifier/kill", processHandler.HandleKillProcess)
	r.GET("/process/:identifier", processHandler.HandleGetProcess)

	// Network routes
	r.GET("/network/process/:pid/ports", networkHandler.HandleGetPorts)
	r.POST("/network/process/:pid/monitor", networkHandler.HandleMonitorPorts)
	r.DELETE("/network/process/:pid/monitor", networkHandler.HandleStopMonitoringPorts)

	// Register WebSocket endpoints for watch and logs stream
	r.GET("/ws/watch/filesystem/*path", fsHandler.HandleWatchDirectoryWebSocket)
	r.GET("/ws/process/:identifier/logs/stream", processHandler.HandleGetProcessLogsStreamWebSocket)

	// Health check route
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	return r
}

// corsMiddleware adds CORS headers to all responses
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
