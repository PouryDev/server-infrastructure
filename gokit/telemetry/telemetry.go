// Package telemetry wires OpenTelemetry traces and metrics to the shared OTLP collector.
//
// It follows the infrastructure contract: OTLP gRPC to OTEL_EXPORTER_OTLP_ENDPOINT, resource
// attributes from OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES, always-on parent-based sampling
// (the collector does the tail sampling), W3C Trace Context + Baggage propagation, periodic metric
// export, and a flush on shutdown. Without an endpoint (or with OTEL_SDK_DISABLED=true) it installs
// nothing, so local runs and tests need no collector.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Config overrides what the standard OTEL_* variables do not cover.
type Config struct {
	// ServiceName is used when OTEL_SERVICE_NAME is not set.
	ServiceName    string
	ServiceVersion string
	// MetricInterval is the export period. Default: OTEL_METRIC_EXPORT_INTERVAL if set, else 30s (the
	// infrastructure docs suggest 30s).
	MetricInterval time.Duration
}

// Shutdown flushes and stops the providers.
type Shutdown func(ctx context.Context) error

// Enabled reports whether telemetry would be exported with the current environment.
func Enabled() bool {
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") {
		return false
	}
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") != ""
}

// Setup installs global tracer, meter and propagator. The returned Shutdown is always non-nil.
func Setup(ctx context.Context, cfg Config) (Shutdown, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	if !Enabled() {
		return func(context.Context) error { return nil }, nil
	}

	// Later options win, so the environment overrides the defaults from Config.
	res, err := resource.New(ctx,
		resource.WithAttributes(defaultAttrs(cfg)...),
		resource.WithProcess(), resource.WithHost(), resource.WithTelemetrySDK(),
		resource.WithFromEnv(), // OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry resource: %w", err)
	}

	traceExp, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("trace exporter: %w", err)
	}
	// ParentBased(AlwaysSample): the application must not sample, the collector does.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	)

	metricExp, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("metric exporter: %w", err)
	}
	// Precedence: Config.MetricInterval, then OTEL_METRIC_EXPORT_INTERVAL (read by the SDK), then 30s.
	var readerOpts []sdkmetric.PeriodicReaderOption
	switch {
	case cfg.MetricInterval > 0:
		readerOpts = append(readerOpts, sdkmetric.WithInterval(cfg.MetricInterval))
	case os.Getenv("OTEL_METRIC_EXPORT_INTERVAL") == "":
		readerOpts = append(readerOpts, sdkmetric.WithInterval(30*time.Second))
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp, readerOpts...)),
		sdkmetric.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	if err := runtime.Start(); err != nil {
		_ = tp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
		return nil, fmt.Errorf("runtime metrics: %w", err)
	}

	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}

func defaultAttrs(cfg Config) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if cfg.ServiceName != "" {
		attrs = append(attrs, semconv.ServiceName(cfg.ServiceName))
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	return attrs
}
