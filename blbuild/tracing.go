package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
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

// traceIDFrom pulls the trace id out of a W3C traceparent
// (version-traceid-spanid-flags). The parent span id is deliberately ignored;
// see where buildContext is set for why.
func traceIDFrom(traceParent string) (trace.TraceID, bool) {
	parts := strings.Split(traceParent, "-")
	if len(parts) < 3 {
		return trace.TraceID{}, false
	}
	id, err := trace.TraceIDFromHex(parts[1])
	if err != nil || !id.IsValid() {
		return trace.TraceID{}, false
	}
	return id, true
}

// executionIDGenerator gives the root span the trace id derived from the step
// function execution, so a build is still reachable from its execution, while
// every span id stays random.
//
// This is how the trace id survives without a parent: the SDK only adopts an
// inherited trace id from a parent span context, and any parent valid enough to
// carry one would have to name a span that exists.
type executionIDGenerator struct {
	traceID trace.TraceID
}

func (g *executionIDGenerator) NewIDs(context.Context) (trace.TraceID, trace.SpanID) {
	return g.traceID, g.randomSpanID()
}

func (g *executionIDGenerator) NewSpanID(context.Context, trace.TraceID) trace.SpanID {
	return g.randomSpanID()
}

func (g *executionIDGenerator) randomSpanID() trace.SpanID {
	var id trace.SpanID
	for !id.IsValid() {
		// crypto/rand.Read never returns a short read without an error, and an
		// error here is unrecoverable in practice; an all-zero id would be
		// rejected as invalid, hence the loop.
		if _, err := rand.Read(id[:]); err != nil {
			warn("telemetry: generating a span id: %v", err)
			return id
		}
	}
	return id
}

func tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// buildSampler keeps every build, always, with no way for anything upstream to
// override it.
//
// AlwaysSample and not ParentBased(AlwaysSample): ParentBased applies its root
// sampler only when there is no parent, and defers to the parent's decision
// when there is one. The platform injects
// OTEL_TRACES_SAMPLER=parentbased_traceidratio with a 0.1 ratio, so the day
// anything upstream is instrumented and arrives unsampled, nine builds in ten
// would silently lose their trace — precisely when someone needs it to debug
// them. A build is a rare, minutes-long event; there is nothing to save here.
//
// Setting a sampler explicitly also makes the SDK ignore OTEL_TRACES_SAMPLER,
// which it only reads when none is set.
func buildSampler() sdktrace.Sampler {
	return sdktrace.AlwaysSample()
}

// buildContext is the context every span descends from. It carries no parent:
// the build is the root of its own trace.
var buildContext context.Context

// InitTracing wires the OTLP exporter and adopts the trace id of the execution
// that started this build.
//
// The traceparent comes from the step function through the targets file. Only
// its trace id is used: one execution still means one trace, so "why was this
// build slow" stays a trace-id lookup rather than a correlation by timestamp,
// but the build's root span has no parent — because the orchestration exports
// no spans here, so any parent it named would be one that never arrives.
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

	tpOpts := []sdktrace.TracerProviderOption{}
	if id, ok := traceIDFrom(traceParent); ok {
		tpOpts = append(tpOpts, sdktrace.WithIDGenerator(&executionIDGenerator{traceID: id}))
	}

	tp := sdktrace.NewTracerProvider(append(tpOpts,
		sdktrace.WithSampler(buildSampler()),
		sdktrace.WithBatcher(exporter,
			// Small batches, short delay: a build can end abruptly, and a span
			// still sitting in the queue is a span nobody will ever see.
			sdktrace.WithBatchTimeout(2*time.Second),
			sdktrace.WithMaxExportBatchSize(64),
		),
		sdktrace.WithResource(res),
	)...)
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

	// Deliberately NOT Extract()ed into a parent context. The traceparent's
	// parent-span-id is a slice of a hash of the execution ARN, so it names a span
	// nothing ever emits: the orchestration does not export to this collector at
	// all. Making `build` its child left every trace flagged "this trace has
	// missing spans", because a collected span referenced a parent no other span
	// matched. The trace id is still adopted, through the ID generator above, so a
	// build is still findable from its execution — it is simply the root of its
	// own trace instead of an orphan hanging off a ghost.
	buildContext = ctx

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
