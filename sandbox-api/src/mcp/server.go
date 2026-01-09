package mcp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"

	"github.com/blaxel-ai/sandbox-api/src/handler"
)

// Server represents the MCP server
type Server struct {
	mcpServer *mcp.Server
	handlers  *Handlers
	engine    *gin.Engine
}

// Handlers contains all the handlers used by the MCP server
type Handlers struct {
	FileSystem *handler.FileSystemHandler
	Process    *handler.ProcessHandler
	Network    *handler.NetworkHandler
}

// NewServer creates a new MCP server using the official SDK
func NewServer(ginEngine *gin.Engine) (*Server, error) {
	logrus.Info("Creating MCP server")

	// Create MCP server with the official SDK
	mcpServer := mcp.NewServer(
		&mcp.Implementation{
			Name:    "Sandbox API Server",
			Version: "1.0.0",
		},
		nil,
	)

	// Initialize handlers
	handlers := &Handlers{
		FileSystem: handler.NewFileSystemHandler(),
		Process:    handler.NewProcessHandler(),
		Network:    handler.NewNetworkHandler(),
	}

	server := &Server{
		mcpServer: mcpServer,
		handlers:  handlers,
		engine:    ginEngine,
	}

	logrus.Info("Registering tools")
	// Register all tools
	if err := server.registerTools(); err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	logrus.Info("Tools registered")

	// Set up HTTP endpoints using the official SDK pattern
	server.setupHTTPEndpoints()

	return server, nil
}

// Serve starts the MCP server
func (s *Server) Serve() error {
	// The server is served via HTTP endpoints through Gin
	return nil
}

// setupHTTPEndpoints sets up the HTTP endpoints using the official SDK pattern
func (s *Server) setupHTTPEndpoints() {
	// Create the streamable HTTP handler using the official SDK
	handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		// Return the MCP server for each request
		return s.mcpServer
	}, nil)

	// Wrap the handler with Gin
	s.engine.Any("/mcp/*path", gin.WrapH(http.StripPrefix("/mcp", handler)))

	// Also handle the base /mcp endpoint without trailing slash
	s.engine.Any("/mcp", gin.WrapH(handler))

	logrus.Info("MCP HTTP endpoints configured at /mcp")
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
func LogToolCall[T any, R any](toolName string, handler func(ctx context.Context, req *mcp.CallToolRequest, args T) (*mcp.CallToolResult, R, error)) func(context.Context, *mcp.CallToolRequest, T) (*mcp.CallToolResult, R, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, args T) (*mcp.CallToolResult, R, error) {
		start := time.Now()
		logrus.Infof("Tool call started: %s", toolName)

		result, output, err := handler(ctx, req, args)

		duration := time.Since(start)
		if err != nil {
			logrus.Errorf("Tool call failed: %s (duration: %v, error: %v)", toolName, duration, err)
			// Ensure error message is never empty to comply with MCP/Claude requirements.
			// Claude's API rejects tool results with is_error=true but empty content.
			if err.Error() == "" {
				err = fmt.Errorf("tool %s failed with unknown error", toolName)
			}
		} else {
			logrus.Infof("Tool call completed: %s (duration: %v)", toolName, duration)
		}

		return result, output, err
	}
}
