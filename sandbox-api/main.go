package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/beamlit/sandbox-api/docs" // swagger generated docs
	"github.com/beamlit/sandbox-api/src/api"
	"github.com/beamlit/sandbox-api/src/mcp"
	"github.com/joho/godotenv"
)

// @title           Sandbox API
// @version         0.0.1-preview
// @description     API for manipulating filesystem, processes and network.

// @host      localhost:8080
// @BasePath  /
func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	docs.SwaggerInfo.Host = fmt.Sprintf("%s:%s", os.Getenv("HOST"), os.Getenv("PORT"))

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

	log.Printf("Port: %d", portValue)
	log.Printf("Command: %s", commandValue)
	log.Printf("Short Command: %s", *shortCommand)

	// Check for command after the flags
	if commandValue != "" {
		// Join all remaining arguments as they may form the command
		log.Printf("Executing command: %s", commandValue)

		// Create the command with the context
		cmd := exec.CommandContext(ctx, "sh", "-c", commandValue)
		cmd.Stdout = log.Writer()
		cmd.Stderr = log.Writer()

		cmd.Dir = "/"

		// Start the command in a goroutine so it doesn't block the server
		go func() {
			// Start the command
			if err := cmd.Start(); err != nil {
				log.Fatalf("Failed to start command: %v", err)
				return
			}
			log.Printf("Command started successfully")

			// Wait for the command to complete
			if err := cmd.Wait(); err != nil {
				// Check if context was cancelled
				select {
				case <-ctx.Done():
					log.Printf("Command was cancelled")
				default:
					log.Printf("Command exited with error: %v", err)
				}
			} else {
				log.Printf("Command completed successfully")
			}
		}()
	}

	// Set up the router with all our API routes
	router := api.SetupRouter()
	mcpServer, err := mcp.NewServer(router)
	if err != nil {
		log.Fatalf("Failed to create MCP server: %v", err)
	}

	// Start the server
	if err := mcpServer.Serve(); err != nil {
		log.Fatalf("Failed to start MCP server: %v", err)
	}

	// Start the server
	serverAddr := fmt.Sprintf(":%d", portValue)
	log.Printf("Starting Sandbox API server on %s", serverAddr)
	if err := router.Run(serverAddr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
