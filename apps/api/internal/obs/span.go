package obs

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// StartDBSpan creates a child span for a database query. The span name follows
// the "<verb>_<resource>" convention (e.g. "get_task", "list_projects"). The
// caller must call span.End() when the operation completes, typically via
// defer.
func StartDBSpan(ctx context.Context, operation string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tracer := otel.Tracer(tracerName)
	allAttrs := make([]attribute.KeyValue, 0, len(attrs)+1)
	allAttrs = append(allAttrs, attribute.String("db.system", "mysql"))
	allAttrs = append(allAttrs, attrs...)
	return tracer.Start(ctx, operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(allAttrs...),
	)
}

// StartMCPSpan creates a child span for an MCP tool invocation. The operation
// string should be the tool name (e.g. "create_task", "search_tasks"). The
// caller must call span.End() when the invocation completes.
func StartMCPSpan(ctx context.Context, operation string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tracer := otel.Tracer(tracerName)
	allAttrs := make([]attribute.KeyValue, 0, len(attrs)+1)
	allAttrs = append(allAttrs, attribute.String("mcp.tool", operation))
	allAttrs = append(allAttrs, attrs...)
	return tracer.Start(ctx, "mcp_"+operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(allAttrs...),
	)
}

// StartAISpan creates a child span for an AI provider call. The operation
// string should describe the action (e.g. "chat_completion",
// "embed_documents"). Provider and model are recorded as span attributes when
// non-empty. The caller must call span.End() when the call completes.
func StartAISpan(ctx context.Context, operation string, provider string, model string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	tracer := otel.Tracer(tracerName)
	allAttrs := make([]attribute.KeyValue, 0, len(attrs)+3)
	if provider != "" {
		allAttrs = append(allAttrs, attribute.String("ai.provider", provider))
	}
	if model != "" {
		allAttrs = append(allAttrs, attribute.String("ai.model", model))
	}
	allAttrs = append(allAttrs, attrs...)
	return tracer.Start(ctx, "ai_"+operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(allAttrs...),
	)
}

// RecordError records an error on the given span if err is non-nil. This is a
// convenience wrapper that avoids nil-check boilerplate at every call site.
func RecordError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
	}
}
