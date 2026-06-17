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

// Init initialises Sentry according to environment configuration.
//
// Control flags:
//
//	SENTRY_ENABLED=false  → opt-out (default is enabled)
//	SENTRY_DSN env var    → overrides build-time DSN; if both empty, Sentry is a no-op
//
// Anonymous mode:
//
//	When BL_ENV is not "prod" or "dev" (OSS / self-hosted), SendDefaultPII is false
//	and user/IP data is stripped from all events.
//
// Returns a flush function to call on graceful shutdown (non-blocking, 2 s max).
func Init() func() {
	if os.Getenv("SENTRY_ENABLED") == "false" {
		logrus.Info("Sentry disabled via SENTRY_ENABLED=false")
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

	blEnv := os.Getenv("BL_ENV")
	isBlaxelCloud := blEnv == "prod" || blEnv == "dev"

	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      blEnv,
		SendDefaultPII:   isBlaxelCloud,
		AttachStacktrace: true,
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			if !isBlaxelCloud {
				event.User = sentry.User{}
				event.Request = nil
			}
			return event
		},
	})
	if err != nil {
		logrus.WithError(err).Warn("Sentry initialisation failed – continuing without Sentry")
		return func() {}
	}

	mode := "anonymous"
	if isBlaxelCloud {
		mode = "identified"
	}
	logrus.Infof("Sentry initialised (env=%s, mode=%s)", blEnv, mode)

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
