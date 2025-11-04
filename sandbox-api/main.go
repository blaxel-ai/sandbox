package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/blaxel-ai/sandbox-api/docs" // swagger generated docs
	"github.com/blaxel-ai/sandbox-api/src/api"
	"github.com/blaxel-ai/sandbox-api/src/mcp"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

// @title           Sandbox API
// @version         0.0.1
// @description     API for manipulating filesystem, processes and network.
// @host            run.blaxel.ai/{workspace_id}/sandboxes/{sandbox_id}
// @schemes         https
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @BasePath        /
func main() {
	logrus.SetFormatter(&logrus.TextFormatter{
		DisableColors: true,
	})
	logrus.SetLevel(logrus.DebugLevel)

	// Load .env file
	_ = godotenv.Load()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workspace := os.Getenv("BL_WORKSPACE")
	name := os.Getenv("BL_NAME")

	if workspace != "" && name != "" {
		docs.SwaggerInfo.BasePath = fmt.Sprintf("/%s/sandboxes/%s", workspace, name)
	}

	if os.Getenv("BL_ENV") == "prod" {
		docs.SwaggerInfo.Host = "run.blaxel.ai"
		docs.SwaggerInfo.Schemes = []string{"https"}
	} else if os.Getenv("BL_ENV") == "dev" {
		docs.SwaggerInfo.Host = "run.blaxel.dev"
		docs.SwaggerInfo.Schemes = []string{"https"}
	} else {
		docs.SwaggerInfo.Host = "localhost:8080"
		docs.SwaggerInfo.BasePath = "/"
		docs.SwaggerInfo.Schemes = []string{"http"}
	}
	gin.SetMode(gin.ReleaseMode)
	// Define command-line flags
	port := flag.Int("port", 8080, "Port to listen on")
	shortPort := flag.Int("p", 8080, "Port to listen on (shorthand)")
	command := flag.String("command", "", "Command to execute")
	shortCommand := flag.String("c", "", "Command to execute (shorthand)")
	flag.Parse()

	// Use the port provided by either flag
	portValue := *port
	if *shortPort != 8080 {
		portValue = *shortPort
	}

	commandValue := *command
	if *shortCommand != "" {
		commandValue = *shortCommand
	}

	logrus.Infof("Port: %d", portValue)
	if os.Getenv("SHELL") != "" {
		logrus.Infof("Shell: %s", os.Getenv("SHELL"))
	}
	if os.Getenv("SHELL_ARGS") != "" {
		logrus.Infof("Shell args: %s", os.Getenv("SHELL_ARGS"))
	}

	// Check for command after the flags
	if commandValue != "" {
		// Join all remaining arguments as they may form the command
		logrus.Infof("Executing command: %s", commandValue)

		// Create the command with the context
		// Use SHELL and SHELL_ARGS environment variables if set
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "sh"
		}

		shellArgs := os.Getenv("SHELL_ARGS")
		if shellArgs == "" {
			shellArgs = "-c"
		}

		// Build command arguments
		cmdArgs := []string{}
		if shellArgs != "" {
			cmdArgs = append(cmdArgs, strings.Fields(shellArgs)...)
		}
		cmdArgs = append(cmdArgs, commandValue)

		cmd := exec.CommandContext(ctx, shell, cmdArgs...)
		cmd.Stdout = logrus.StandardLogger().Out
		cmd.Stderr = logrus.StandardLogger().Out

		cmd.Dir = "/"

		// Start the command in a goroutine so it doesn't block the server
		go func() {
			// Start the command
			if err := cmd.Start(); err != nil {
				logrus.Fatalf("Failed to start command: %v", err)
				return
			}
			logrus.Infof("Command started successfully")

			// Wait for the command to complete
			if err := cmd.Wait(); err != nil {
				// Check if context was cancelled
				select {
				case <-ctx.Done():
					logrus.Infof("Command was cancelled")
				default:
					logrus.Infof("Command exited with error: %v", err)
				}
			} else {
				logrus.Infof("Command completed successfully")
			}
		}()
	}

	// Set up the router with all our API routes
	router := api.SetupRouter()
	mcpServer, err := mcp.NewServer(router)
	if err != nil {
		logrus.Fatalf("Failed to create MCP server: %v", err)
	}
	// Start the server
	if err := mcpServer.Serve(); err != nil {
		logrus.Fatalf("Failed to start MCP server: %v", err)
	}

	// Start the server
	serverAddr := fmt.Sprintf(":%d", portValue)
	logrus.Infof("Starting Sandbox API server on %s", serverAddr)
	if err := router.Run(serverAddr); err != nil {
		logrus.Fatalf("Failed to start server: %v", err)
	}
}
