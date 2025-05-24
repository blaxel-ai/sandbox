package api

import (
	"fmt"
	"math"
	"net/http"
	"os"
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
func SetupRouter() *gin.Engine {
	// Initialize the router
	r := gin.New()

	// Add recovery middleware
	r.Use(gin.Recovery())

	// Add middleware for CORS
	r.Use(corsMiddleware())

	// Add logrus middleware
	r.Use(logrusMiddleware())

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
			} else if method == "DELETE" {
				fsHandler.HandleDeleteTree(c)
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

func logrusMiddleware() gin.HandlerFunc {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknow"
	}
	var skip map[string]struct{}

	return func(c *gin.Context) {
		// other handler can change c.Path so:
		path := c.Request.URL.Path
		start := time.Now()
		c.Next()
		stop := time.Since(start)
		latency := int(math.Ceil(float64(stop.Nanoseconds()) / 1000000.0))
		statusCode := c.Writer.Status()
		clientIP := c.ClientIP()
		clientUserAgent := c.Request.UserAgent()
		referer := c.Request.Referer()
		dataLength := c.Writer.Size()
		if dataLength < 0 {
			dataLength = 0
		}

		if _, ok := skip[path]; ok {
			return
		}

		entry := logrus.WithFields(logrus.Fields{
			"hostname":   hostname,
			"statusCode": statusCode,
			"latency":    latency, // time to process
			"clientIP":   clientIP,
			"method":     c.Request.Method,
			"path":       path,
			"referer":    referer,
			"size":       dataLength,
			"userAgent":  clientUserAgent,
		})

		if len(c.Errors) > 0 {
			entry.Error(c.Errors.ByType(gin.ErrorTypePrivate).String())
		} else {
			msg := fmt.Sprintf("%s %s %d %d (%dms)", c.Request.Method, path, statusCode, dataLength, latency)
			if statusCode >= http.StatusInternalServerError {
				entry.Error(msg)
			} else if statusCode >= http.StatusBadRequest {
				entry.Warn(msg)
			} else {
				entry.Info(msg)
			}
		}
	}
}
