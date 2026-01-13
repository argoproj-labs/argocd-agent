// Copyright 2025 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tracing

import (
	"context"
	"fmt"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// TracerName is the name of the tracer used throughout the application
	TracerName = "github.com/argoproj-labs/argocd-agent"

	// CloudEvents Distributed Tracing extension attributes
	// https://github.com/cloudevents/spec/blob/main/cloudevents/extensions/distributed-tracing.md
	traceParentKey string = "traceparent"
	traceStateKey  string = "tracestate"
)

var (
	// globalTracer is the global tracer instance
	globalTracer trace.Tracer
	// globalTracerProvider is the global tracer provider
	globalTracerProvider *sdktrace.TracerProvider
)

// InitTracing initializes the OpenTelemetry tracing with the given configuration
func InitTracer(ctx context.Context, serviceName, otlpAddress string, otlpInsecure bool) (func(context.Context) error, error) {
	// Create resource with service name
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Set up OTLP exporter
	var exporter *otlptrace.Exporter
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(otlpAddress),
	}

	if otlpInsecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	// Use timeout context for exporter creation to prevent startup hang
	// if OTLP collector is unreachable
	exporterCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	exporter, err = otlptracegrpc.New(exporterCtx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create tracer provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set global tracer provider
	otel.SetTracerProvider(tp)

	// Set global propagator for context propagation across service boundaries
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	globalTracerProvider = tp
	globalTracer = otel.Tracer(TracerName)

	// Return shutdown function
	return tp.Shutdown, nil
}

// Tracer returns the global tracer instance
func Tracer() trace.Tracer {
	if globalTracer == nil {
		return otel.Tracer(TracerName)
	}
	return globalTracer
}

// IsEnabled returns true if tracing is enabled (not using no-op tracer)
func IsEnabled() bool {
	return globalTracerProvider != nil
}

// RecordError records an error on the span and sets the span status to Error.
func RecordError(span trace.Span, err error) {
	if err == nil || !IsEnabled() {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// SetSpanOK explicitly sets the span status to OK.
// Use this to mark successful completion after error checks.
func SetSpanOK(span trace.Span) {
	if !IsEnabled() {
		return
	}
	span.SetStatus(codes.Ok, "")
}

// InjectTraceContext injects trace context from the given context into the CloudEvent
// using the CloudEvents Distributed Tracing extension as defined in:
// https://github.com/cloudevents/spec/blob/main/cloudevents/extensions/distributed-tracing.md
// This allows trace propagation across the event stream. Only operates when tracing is enabled.
func InjectTraceContext(ctx context.Context, ev *cloudevents.Event) {
	if !IsEnabled() {
		return
	}

	// Inject trace context into the CloudEvent
	carrier := make(propagation.MapCarrier)
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(ctx, carrier)
	if len(carrier) == 0 {
		return
	}

	if traceparent, ok := carrier[traceParentKey]; ok && traceparent != "" {
		ev.SetExtension(traceParentKey, traceparent)
	}

	if tracestate, ok := carrier[traceStateKey]; ok && tracestate != "" {
		ev.SetExtension(traceStateKey, tracestate)
	}
}

// ExtractTraceContext extracts trace context from the CloudEvent and returns
// a new context with the trace context attached using the CloudEvents Distributed
// Tracing extension as defined in:
// https://github.com/cloudevents/spec/blob/main/cloudevents/extensions/distributed-tracing.md
// If tracing is not enabled or no trace context exists in the event, returns the original context.
func ExtractTraceContext(ctx context.Context, ev *cloudevents.Event) context.Context {
	if !IsEnabled() {
		return ctx
	}

	// Extract CloudEvents Distributed Tracing extension attributes
	carrier := make(propagation.MapCarrier)

	if traceparent, ok := ev.Extensions()[traceParentKey]; ok {
		if tp, ok := traceparent.(string); ok && tp != "" {
			carrier[traceParentKey] = tp
		}
	}

	if tracestate, ok := ev.Extensions()[traceStateKey]; ok {
		if ts, ok := tracestate.(string); ok && ts != "" {
			carrier[traceStateKey] = ts
		}
	}

	// If no trace context found, return original context
	if len(carrier) == 0 {
		return ctx
	}

	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}

// PopulateSpanFromObject populates the span with the attributes from the given object.
func PopulateSpanFromObject(span trace.Span, obj runtime.Object) {
	gvk := obj.GetObjectKind().GroupVersionKind()

	metaObj, ok := obj.(metav1.Object)
	if !ok {
		span.SetAttributes(AttrResourceKind.String(gvk.Kind))
		return
	}

	span.SetAttributes(AttrResourceName.String(metaObj.GetName()))
	span.SetAttributes(AttrResourceKind.String(gvk.Kind))
	span.SetAttributes(AttrResourceUID.String(string(metaObj.GetUID())))
	span.SetAttributes(AttrNamespace.String(metaObj.GetNamespace()))
}
