package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/blaxel-ai/sandbox-api/docs" // swagger generated docs
	"github.com/blaxel-ai/sandbox-api/src/api"
	"github.com/getsentry/sentry-go"

	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/blaxel-ai/sandbox-api/src/lib/blaxel"
	"github.com/blaxel-ai/sandbox-api/src/lib/networking"
	"github.com/blaxel-ai/sandbox-api/src/lib/proxy"
	"github.com/blaxel-ai/sandbox-api/src/lib/sentrylib"
	"github.com/blaxel-ai/sandbox-api/src/mcp"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/sirupsen/logrus"
)

// @title           Sandbox API
// @version         0.0.1
// @description     API for manipulating filesystem, processes and network.
// @host            sbx-{sandbox_id}-{workspace_id}.{region}.bl.run
// @schemes         https
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @BasePath        /
func main() {
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logrus.SetLevel(logrus.DebugLevel)

	// Load .env file
	_ = godotenv.Load()

	// Define command-line flags
	port := flag.Int("port", 8080, "Port to listen on")
	shortPort := flag.Int("p", 8080, "Port to listen on (shorthand)")
	command := flag.String("command", "", "Command to execute")
	shortCommand := flag.String("c", "", "Command to execute (shorthand)")
	disableTelemetry := flag.Bool("disable-telemetry", false, "Disable anonymous error reporting")
	flag.Parse()

	sentrylib.Version = handler.Version
	sentryFlush := sentrylib.Init(*disableTelemetry)
	defer sentryFlush()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sentrylib.InitMeter(ctx)
	startupStart := time.Now()

	// Resolve {{file(...)}} directives in HTTP_PROXY / HTTPS_PROXY and
	// start a background goroutine that re-reads the token file periodically
	// so rotated credentials are picked up before they expire.
	proxy.StartProxyTokenRefresh(ctx)

	// Parallel: all four tasks are independent of each other
	pm := process.GetProcessManager()
	txn := sentry.StartSpan(ctx, "startup")
	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		span := txn.StartChild("startup.merge_ca_bundle")
		defer span.Finish()
		if err := proxy.MergeCABundle(); err != nil {
			logrus.WithError(err).Error("Failed to merge CA bundle – TLS connections through the proxy may fail")
		}
	}()
	go func() {
		defer wg.Done()
		span := txn.StartChild("startup.wireguard")
		defer span.Finish()
		if err := networking.StartWireGuardFromEnv(); err != nil {
			logrus.WithError(err).Warn("WireGuard initialization failed - the sandbox will NOT have outbound internet connectivity (no egress). Inbound connections to the sandbox will still work. You can check the tunnel status via the /network/tunnel endpoints.")
		}
	}()
	go func() {
		defer wg.Done()
		span := txn.StartChild("startup.scale_reset")
		defer span.Finish()
		if err := blaxel.ScaleReset(); err != nil {
			logrus.Warnf("Failed to reset scale-to-zero counter on startup: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		span := txn.StartChild("startup.load_state")
		defer span.Finish()
		if err := pm.LoadState(); err != nil {
			logrus.WithError(err).Warn("Failed to load process state from disk")
		}
	}()

	wg.Wait()
	txn.Finish()
	sentrylib.DistributionMetric("sandbox.startup_duration", float64(time.Since(startupStart).Milliseconds()), sentry.UnitMillisecond)

	// Swagger docs setup
	blEnv := os.Getenv("BL_ENV")
	workspace := os.Getenv("BL_WORKSPACE")
	name := os.Getenv("BL_NAME")

	if workspace != "" && name != "" {
		docs.SwaggerInfo.BasePath = fmt.Sprintf("/%s/sandboxes/%s", workspace, name)
	}

	if blEnv == "prod" {
		docs.SwaggerInfo.Host = "run.blaxel.ai"
		docs.SwaggerInfo.Schemes = []string{"https"}
	} else if blEnv == "dev" {
		docs.SwaggerInfo.Host = "run.blaxel.dev"
		docs.SwaggerInfo.Schemes = []string{"https"}
	} else {
		docs.SwaggerInfo.Host = "localhost:8080"
		docs.SwaggerInfo.BasePath = "/"
		docs.SwaggerInfo.Schemes = []string{"http"}
	}

	gin.SetMode(gin.ReleaseMode)
	disableRequestLogging := os.Getenv("DISABLE_REQUEST_LOGGING") == "true"
	enableProcessingTime := os.Getenv("ENABLE_PROCESSING_TIME") == "true"

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

	// Start background command if specified
	if commandValue != "" {
		startBackgroundCommand(ctx, commandValue)
	}

	// Set up the router with all our API routes
	router := api.SetupRouter(disableRequestLogging, enableProcessingTime)

	// Route registration happens inside the NewServer constructor
	if _, err := mcp.NewServer(router); err != nil {
		logrus.Fatalf("Failed to create MCP server: %v", err)
	}

	// Start the server with custom timeout configuration for large file uploads
	serverAddr := fmt.Sprintf(":%d", portValue)
	logrus.Infof("Starting Sandbox API server on %s", serverAddr)

	server := &http.Server{
		Addr:              serverAddr,
		Handler:           router,
		ReadTimeout:       10 * time.Minute, // Allow up to 10 minutes for reading large uploads
		WriteTimeout:      10 * time.Minute, // Allow up to 10 minutes for writing large downloads
		ReadHeaderTimeout: 30 * time.Second, // Headers should be quick
		IdleTimeout:       2 * time.Minute,  // Keep-alive connections timeout
		MaxHeaderBytes:    1 << 20,          // 1 MB max header size
	}

	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logrus.Infof("Received signal %v, shutting down...", sig)

		// Shutdown HTTP server gracefully with a timeout
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logrus.WithError(err).Warn("Failed to shutdown HTTP server gracefully")
		}

		// Stop WireGuard client and clean up routes
		if err := networking.StopWireGuard(); err != nil {
			logrus.WithError(err).Debug("WireGuard shutdown")
		}

		// Cancel the main context (stops background command if any)
		cancel()
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logrus.Fatalf("Failed to start server: %v", err)
	}

	logrus.Info("Server stopped")
}

// startBackgroundCommand runs the given command string in a goroutine using the
// configured SHELL and SHELL_ARGS environment variables.
func startBackgroundCommand(ctx context.Context, command string) {
	logrus.Infof("Executing command: %s", command)

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
	cmdArgs = append(cmdArgs, command)

	cmd := exec.CommandContext(ctx, shell, cmdArgs...)
	cmd.Stdout = logrus.StandardLogger().Out
	cmd.Stderr = logrus.StandardLogger().Out
	cmd.Dir = "/"

	// Start the command in a goroutine so it doesn't block the server
	go func() {
		if err := cmd.Start(); err != nil {
			logrus.Fatalf("Failed to start command: %v", err)
			return
		}
		logrus.Infof("Command started successfully")

		if err := cmd.Wait(); err != nil {
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
