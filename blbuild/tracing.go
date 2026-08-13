package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "blbuild"

func tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// buildContext carries the trace context extracted from the targets file, so
// every span the build creates hangs off the orchestration that started it.
var buildContext context.Context

// InitTracing wires the OTLP exporter and adopts the caller's trace context.
//
// The traceparent comes from the step function through the targets file: without
// it a build is an orphan trace and answering "why was this build slow" means
// correlating by timestamp. With it, the build is a subtree of its execution.
//
// Returns a shutdown function that flushes pending spans. A build is short-lived
// and gets SIGKILLed with its sandbox, so an unflushed batch is a lost trace.
func InitTracing(ctx context.Context, traceParent string) (func(), error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT is not set")
	}

	// The collector rejects an unauthenticated write with a 401, and the SDK
	// drops the batch without failing the build — which is how a fully
	// instrumented build produced no traces at all. The headers are the ones
	// @blaxel/telemetry sends; the credential is injected into this sandbox by
	// the control plane for exactly this purpose and is scoped to the workspace
	// the build is for.
	opts := []otlptracehttp.Option{}
	headers := map[string]string{}
	if token := os.Getenv("BL_API_KEY"); token != "" {
		headers["x-blaxel-authorization"] = "Bearer " + token
	}
	if workspace := os.Getenv("BL_WORKSPACE"); workspace != "" {
		headers["x-blaxel-workspace"] = workspace
	}
	if len(headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(headers))
	} else {
		warn("no telemetry credential in the environment; the collector will reject these spans")
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("blbuild"),
			semconv.ServiceVersion(version()),
		),
		resource.WithFromEnv(),
	)
	if err != nil {
		// A resource we could not fully build is still better than no traces.
		res = resource.Default()
	}

	tp := sdktrace.NewTracerProvider(
		// Never sampled away. The platform injects
		// OTEL_TRACES_SAMPLER=parentbased_traceidratio with a 0.1 ratio, which the
		// Go SDK reads by default: a build with no sampled parent then had one
		// chance in ten of being kept, and a build is a rare, minutes-long event
		// whose trace is the whole point of instrumenting it.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithBatcher(exporter,
			// Small batches, short delay: a build can end abruptly, and a span
			// still sitting in the queue is a span nobody will ever see.
			sdktrace.WithBatchTimeout(2*time.Second),
			sdktrace.WithMaxExportBatchSize(64),
		),
		sdktrace.WithResource(res),
	)
	// The SDK swallows export failures by default: a collector that rejects every
	// batch looks exactly like a build with no telemetry, which is how a fully
	// instrumented build went unnoticed for hours. Surface them in the build log,
	// which is readable without any access to the telemetry backend.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		warn("telemetry: %v", err)
	}))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))

	buildContext = ctx
	if traceParent != "" {
		buildContext = otel.GetTextMapPropagator().Extract(ctx,
			propagation.MapCarrier{"traceparent": traceParent})
	}

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tp.Shutdown(shutdownCtx); err != nil {
			warn("flushing traces: %v", err)
		}
	}, nil
}

// TraceContext returns the context spans should descend from.
func TraceContext(fallback context.Context) context.Context {
	if buildContext != nil {
		return buildContext
	}
	return fallback
}

// buildAttributes are the identifying attributes put on the root span.
//
// The build identifier goes on the span, never on a metric: it is unique per
// build, so as a metric label it would multiply the time series without bound.
func buildAttributes(t *Targets) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("build.artefact_key", t.Initrd.Key),
		attribute.Int("build.upload_flows", t.UploadFlows),
		attribute.Int("build.part_size_mib", t.Initrd.PartSizeMiB),
	}
}

func version() string {
	if v := os.Getenv("BLBUILD_VERSION"); v != "" {
		return v
	}
	return "dev"
}
