// Package eventbus provides OTel trace context propagation over message attributes.
package eventbus

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// attributeCarrier implements propagation.TextMapCarrier over a map[string]string,
// enabling W3C TraceContext inject/extract through Pub/Sub message attributes.
type attributeCarrier struct {
	attrs map[string]string
}

var _ propagation.TextMapCarrier = (*attributeCarrier)(nil)

func (c *attributeCarrier) Get(key string) string {
	if c.attrs == nil {
		return ""
	}
	return c.attrs[key]
}

func (c *attributeCarrier) Set(key, value string) {
	if c.attrs != nil {
		c.attrs[key] = value
	}
}

func (c *attributeCarrier) Keys() []string {
	keys := make([]string, 0, len(c.attrs))
	for k := range c.attrs {
		keys = append(keys, k)
	}
	return keys
}

// InjectTraceContext injects the span context from ctx into message attributes.
func InjectTraceContext(ctx context.Context, attrs map[string]string) {
	otel.GetTextMapPropagator().Inject(ctx, &attributeCarrier{attrs: attrs})
}

// ExtractTraceContext extracts trace context from message attributes into a new context.
// Returns the original context if attrs is nil.
func ExtractTraceContext(ctx context.Context, attrs map[string]string) context.Context {
	if attrs == nil {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, &attributeCarrier{attrs: attrs})
}
