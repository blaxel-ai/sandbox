package sentrylib

import (
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/sirupsen/logrus"
)

// DSN is injected at build time via:
//
//	-ldflags "-X github.com/blaxel-ai/sandbox-api/src/lib/sentrylib.DSN=<value>"
//
// It can be overridden at runtime by setting the SENTRY_DSN environment variable.
var DSN = ""

// Environment is injected at build time via:
//
//	-ldflags "-X github.com/blaxel-ai/sandbox-api/src/lib/sentrylib.Environment=<value>"
//
// Set to "prod" for main branch builds, "dev" for develop branch builds.
// Falls back to BL_ENV at runtime if empty.
var Environment = ""

// Version is set by the caller before Init to attach release info to events.
var Version = "dev"

// Init initialises Sentry according to environment configuration.
//
// Control flags:
//
//	disabled parameter      → opt-out via --disable-telemetry CLI flag
//	TELEMETRY_ENABLED=false → opt-out via environment variable
//	SENTRY_DSN env var      → overrides build-time DSN; if both empty, Sentry is a no-op
//
// PII is never collected (SendDefaultPII is always false).
//
// Returns a flush function to call on graceful shutdown (non-blocking, 2 s max).
func Init(disabled bool) func() {
	if disabled || os.Getenv("TELEMETRY_ENABLED") == "false" {
		logrus.Info("Telemetry is disabled.")
		return func() {}
	}

	// Env var takes precedence over build-time value
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		dsn = DSN
	}
	if dsn == "" {
		return func() {}
	}

	env := Environment
	if env == "" {
		env = os.Getenv("BL_ENV")
	}
	traceRate := 0.01
	if env == "dev" {
		traceRate = 1.0
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      env,
		Release:          "sandbox-api@" + Version,
		AttachStacktrace: true,
		EnableTracing:    true,
		TracesSampleRate: traceRate,
	})
	if err != nil {
		logrus.WithError(err).Warn("Sentry initialisation failed – continuing without Sentry")
		return func() {}
	}

	// Set global tags for all events
	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag("sandbox.env", env)
		scope.SetTag("sandbox.version", Version)
		if name := os.Getenv("BL_NAME"); name != "" {
			scope.SetTag("sandbox.name", name)
		}
		if workspace := os.Getenv("BL_WORKSPACE"); workspace != "" {
			scope.SetTag("sandbox.workspace", workspace)
		}
	})

	logrus.Infof("")
	logrus.Infof("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	logrus.Infof("  Telemetry is ENABLED (anonymous — no PII collected)")
	logrus.Infof("  This helps the Blaxel team detect and fix crashes faster.")
	logrus.Infof("  No personal data, file contents, or process output is ever sent.")
	logrus.Infof("")
	logrus.Infof("  To opt out, use any of the following:")
	logrus.Infof("    • Run with --disable-telemetry flag")
	logrus.Infof("    • Set TELEMETRY_ENABLED=false in your environment")
	logrus.Infof("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	logrus.Infof("")

	return func() {
		defer func() { _ = recover() }()
		sentry.Flush(2 * time.Second)
	}
}

// CaptureException sends an error to Sentry in a non-blocking, panic-safe way.
func CaptureException(err error) {
	if err == nil {
		return
	}
	go func() {
		defer func() { _ = recover() }()
		sentry.CaptureException(err)
	}()
}

