package mcp

import (
	"fmt"

	"github.com/gin-gonic/gin"
	mcp_golang "github.com/metoro-io/mcp-golang"

	"github.com/beamlit/sandbox-api/src/handler"
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
	fmt.Println("Creating MCP server")
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

	fmt.Println("Registering tools")
	// Register all tools
	err := server.registerTools()
	if err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	fmt.Println("Tools registered")

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

	// Filesystem tools
	if err := s.registerFileSystemTools(); err != nil {
		return err
	}

	// Network tools
	if err := s.registerNetworkTools(); err != nil {
		return err
	}

	return nil
}
