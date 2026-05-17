// Package obs wires the gateway's OpenTelemetry tracing. The
// TracerProvider is configured via NF_OTEL_* environment variables and
// registered as the global provider so any future instrumentation
// library picks it up.
//
// This is a near-verbatim copy of apps/flow-worker/internal/obs/tracer.go.
// It should be extracted to packages/go-shared/obs in a follow-up; the
// P8-1 scaffold keeps it inline to avoid touching flow-worker in this
// task.
package obs

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TracerConfig controls the OTel TracerProvider setup.
type TracerConfig struct {
	// Endpoint is the OTLP HTTP endpoint (e.g. "localhost:4318").
	// When empty, tracing is disabled and InitTracer returns a no-op
	// provider so call sites that take otel.Tracer("...") still compile.
	Endpoint string
	// ServiceName is reported as service.name in all spans.
	ServiceName string
	// ServiceVersion is reported as service.version in all spans.
	ServiceVersion string
	// Insecure disables TLS for the OTLP exporter. Useful for local
	// development against a sidecar collector.
	Insecure bool
}

// InitTracer creates and registers a global TracerProvider backed by an
// OTLP HTTP exporter. When cfg.Endpoint is empty no exporter is
// configured and the returned shutdown func is a no-op. Callers must
// invoke the returned function during graceful shutdown to flush
// pending spans.
func InitTracer(ctx context.Context, cfg TracerConfig) (shutdown func(context.Context) error, err error) {
	if cfg.Endpoint == "" {
		noop := sdktrace.NewTracerProvider()
		otel.SetTracerProvider(noop)
		return func(context.Context) error { return nil }, nil
	}

	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(cfg.Endpoint),
	}
	if cfg.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.version", cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown, nil
}
