package mcp

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	mcp_golang "github.com/metoro-io/mcp-golang"
	"github.com/sirupsen/logrus"

	"github.com/blaxel-ai/sandbox-api/src/handler"
)

// Server represents the MCP server
type Server struct {
	mcpServer *mcp_golang.Server
	handlers  *Handlers
}

// Handlers contains all the handlers used by the MCP server
type Handlers struct {
	FileSystem *handler.FileSystemHandler
	Process    *handler.ProcessHandler
	Network    *handler.NetworkHandler
}

// NewServer creates a new MCP server
func NewServer(gin *gin.Engine) (*Server, error) {
	logrus.Info("Creating MCP server")
	transport := NewWebSocketTransport(gin)
	mcpServer := mcp_golang.NewServer(transport, mcp_golang.WithName("Sandbox API Server"))

	// Initialize handlers
	handlers := &Handlers{
		FileSystem: handler.NewFileSystemHandler(),
		Process:    handler.NewProcessHandler(),
		Network:    handler.NewNetworkHandler(),
	}

	server := &Server{
		mcpServer: mcpServer,
		handlers:  handlers,
	}

	logrus.Info("Registering tools")
	// Register all tools
	err := server.registerTools()
	if err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	logrus.Info("Tools registered")

	return server, nil
}

// Serve starts the MCP server
func (s *Server) Serve() error {
	return s.mcpServer.Serve()
}

// registerTools registers all the tools with the MCP server
func (s *Server) registerTools() error {
	// Process tools
	if err := s.registerProcessTools(); err != nil {
		return err
	}
	logrus.Info("Process tools registered")
	// Filesystem tools
	if err := s.registerFileSystemTools(); err != nil {
		return err
	}
	logrus.Info("Filesystem tools registered")

	// Codegen tools
	if err := s.registerCodegenTools(); err != nil {
		return err
	}
	logrus.Info("Codegen tools registered")
	return nil
}

// LogToolCall wraps a tool handler function with logging middleware
func LogToolCall[T any](toolName string, handler func(T) (*mcp_golang.ToolResponse, error)) func(T) (*mcp_golang.ToolResponse, error) {
	return func(args T) (*mcp_golang.ToolResponse, error) {
		startTime := time.Now()
		logrus.WithFields(logrus.Fields{
			"tool": toolName,
			"args": args,
		}).Info("Tool call started")

		response, err := handler(args)

		duration := time.Since(startTime)
		logEntry := logrus.WithFields(logrus.Fields{
			"tool":        toolName,
			"duration":    duration.String(),
			"duration_ms": duration.Milliseconds(),
		})

		if err != nil {
			logEntry.WithError(err).Error("Tool call failed")
		} else {
			logEntry.Info("Tool call completed successfully")
		}

		return response, err
	}
}
