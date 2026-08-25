package main

import (
	"context"
	"errors"
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
	"github.com/blaxel-ai/sandbox-api/src/handler/archive"
	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/blaxel-ai/sandbox-api/src/lib/blaxel"
	"github.com/blaxel-ai/sandbox-api/src/lib/envfile"
	"github.com/blaxel-ai/sandbox-api/src/lib/identity"
	"github.com/blaxel-ai/sandbox-api/src/lib/networking"
	"github.com/blaxel-ai/sandbox-api/src/lib/oom"
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

	oom.ProtectSelf()
	oom.LimitHeap()

	// Adopt the environment the guest received as a file rather than on its
	// kernel command line, before anything reads the environment or spawns a
	// process. The image's init has normally done it already; doing it here as
	// well means an init that did not is no longer an environment the user
	// silently lost.
	if loaded, err := envfile.Load(); err != nil {
		logrus.WithError(err).WithField("loaded", loaded).Errorf("Failed to load part of the environment from %s - those user environment variables are missing", envfile.PathVar)
	} else if loaded > 0 {
		logrus.WithField("count", loaded).Infof("Loaded environment variables from %s that were missing from the process environment", envfile.PathVar)
	}

	// Define command-line flags
	port := flag.Int("port", 8080, "Port to listen on")
	shortPort := flag.Int("p", 8080, "Port to listen on (shorthand)")
	command := flag.String("command", "", "Command to execute")
	shortCommand := flag.String("c", "", "Command to execute (shorthand)")
	disableTelemetry := flag.Bool("disable-telemetry", false, "Disable anonymous error reporting")
	workloadUser := flag.String("user", "", "Run processes, terminals and filesystem operations as this user, in Docker USER syntax (also settable with "+identity.EnvUser+", which needs "+identity.EnvEnabled+")")
	flag.Parse()

	// Resolve the workload identity before anything can spawn a process, so a
	// misconfigured user fails at boot instead of at first exec.
	identity.SetSpec(*workloadUser)
	identity.Get()

	sentrylib.Version = handler.Version
	sentryFlush := sentrylib.Init(*disableTelemetry)
	defer sentryFlush()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sentrylib.InitMeter(ctx)
	startupStart := time.Now()

	// Point HTTP_PROXY / HTTPS_PROXY at a loopback proxy that injects the
	// identity token on every request, so rotated credentials are picked up by
	// every process, including ones started before the rotation.
	proxy.Start(ctx)

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

	// An archive interrupted by a crash or an upgrade leaves the root read-only,
	// and nothing else of its state survives the restart. Adopt the filesystem's
	// own state first, so the sandbox says it is frozen instead of failing every
	// write while reporting itself healthy.
	archive.AdoptRootState()

	// Restore an archived filesystem before anything of the workload runs, so
	// the command below and the relaunched processes see the restored files
	// rather than race the extraction. It is a no-op unless the sandbox was
	// started with an archive to import, and it happens once per filesystem:
	// see archive.DefaultImportMarker.
	restored := importArchive(ctx)

	// Start background command if specified, unless the sandbox is frozen: on a
	// read-only root every write of the workload fails, and starting it there
	// only buries the reason under its own errors. Resuming the sandbox is what
	// makes it startable, and the operator does that knowingly.
	if commandValue != "" && restored && !archive.Quiesced() {
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

	server := newHTTPServer(serverAddr, router)

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

// newHTTPServer builds the API server.
//
// ReadTimeout and WriteTimeout are deliberately left at 0 (no deadline). They
// are NOT idle timeouts: net/http arms them once, when the request starts, and
// fires them regardless of traffic on the connection. Any non-zero value is
// therefore a hard cap on the lifetime of every long-lived endpoint we serve:
//   - GET  /process/:identifier/logs/stream
//   - POST /process with Accept: text/event-stream
//   - GET  /watch/filesystem/*path
//   - GET  /terminal/ws (the deadline outlives the WebSocket hijack)
//
// A 10 minute WriteTimeout used to be set here for large file transfers; it cut
// every one of those streams at exactly 600s while the underlying process kept
// running, and the 30s "[keepalive]" lines could not prevent it. Slow-client
// protection is kept where it does not conflict with streaming: ReadHeaderTimeout
// bounds header slowloris, IdleTimeout bounds idle keep-alive connections.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second, // Headers should be quick
		IdleTimeout:       2 * time.Minute,  // Keep-alive connections timeout
		MaxHeaderBytes:    1 << 20,          // 1 MB max header size
	}
}

// importArchive restores the filesystem of an archived sandbox, if this one was
// given one to restore and has not restored it yet.
//
// A failure before anything was written is not fatal: the sandbox boots without
// the archived data instead of not booting at all, which leaves an operator
// something to look at and retry from. It is synchronous — the whole point is
// that the workload starts on top of the restored filesystem.
//
// It returns false when the workload must not be started: a failure that already
// wrote part of the archive leaves a filesystem that is neither the image's nor
// the archived sandbox's, and running the workload on it would write more state
// on top of a state that never existed. The sandbox stays up, frozen, so the
// failure is visible through /archive/status rather than silently absorbed.
func importArchive(ctx context.Context) bool {
	result, err := archive.ImportOnBoot(ctx)
	if errors.Is(err, archive.ErrNoImport) {
		return true
	}
	if errors.Is(err, archive.ErrPartialImport) {
		logrus.WithError(err).Error("The archive this sandbox was started from was only partially restored - the workload is not started and the filesystem is frozen")
		if err := archive.Quarantine("failed archive import"); err != nil {
			logrus.WithError(err).Error("Failed to freeze the sandbox after a partial import")
		}
		return false
	}
	if err != nil {
		logrus.WithError(err).Error("Failed to restore the archive this sandbox was started from - it boots with the filesystem of its image instead")
		return true
	}
	logrus.WithFields(logrus.Fields{
		"restored":   result.Restored,
		"deleted":    result.Deleted,
		"relaunched": len(result.Relaunched),
		"duration":   result.Duration,
	}).Info("Restored the sandbox filesystem from an archive")
	return true
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
	cmd.Env = identity.Get().DecorateEnv(os.Environ())
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: identity.Get().Credential()}

	// Start the command in a goroutine so it doesn't block the server
	go func() {
		if err := cmd.Start(); err != nil {
			logrus.Fatalf("Failed to start command: %v", err)
			return
		}
		oom.PreferAsVictim(cmd.Process.Pid)
		// The process manager never hears about this command, and an archive
		// export has to stop it like any other process: it is usually the
		// workload, so leaving it running would let it write to the filesystem
		// while the archive is read.
		archive.RegisterStartupWorkload(cmd.Process.Pid)
		defer archive.UnregisterStartupWorkload(cmd.Process.Pid)
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
