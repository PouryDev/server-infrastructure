<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Telemetry;

use OpenTelemetry\API\Baggage\Propagation\BaggagePropagator;
use OpenTelemetry\API\Common\Time\Clock;
use OpenTelemetry\API\Metrics\CounterInterface;
use OpenTelemetry\API\Metrics\HistogramInterface;
use OpenTelemetry\API\Trace\Propagation\TraceContextPropagator;
use OpenTelemetry\API\Trace\TracerInterface;
use OpenTelemetry\Context\Propagation\MultiTextMapPropagator;
use OpenTelemetry\Context\Propagation\TextMapPropagatorInterface;
use OpenTelemetry\Contrib\Otlp\MetricExporter;
use OpenTelemetry\Contrib\Otlp\OtlpHttpTransportFactory;
use OpenTelemetry\Contrib\Otlp\SpanExporter;
use OpenTelemetry\SDK\Common\Attribute\Attributes;
use OpenTelemetry\SDK\Metrics\MeterProvider;
use OpenTelemetry\SDK\Metrics\MeterProviderInterface;
use OpenTelemetry\SDK\Metrics\MetricExporterInterface;
use OpenTelemetry\SDK\Metrics\MetricReader\ExportingReader;
use OpenTelemetry\SDK\Resource\ResourceInfo;
use OpenTelemetry\SDK\Resource\ResourceInfoFactory;
use OpenTelemetry\SDK\Trace\Sampler\AlwaysOnSampler;
use OpenTelemetry\SDK\Trace\Sampler\ParentBased;
use OpenTelemetry\SDK\Trace\SpanExporterInterface;
use OpenTelemetry\SDK\Trace\SpanProcessor\BatchSpanProcessor;
use OpenTelemetry\SDK\Trace\SpanProcessor\SimpleSpanProcessor;
use OpenTelemetry\SDK\Trace\TracerProvider;
use OpenTelemetry\SemConv\ResourceAttributes;

/**
 * OpenTelemetry following the infra contract: OTLP to the collector, always-on parent-based sampling (the
 * collector does the tail sampling), W3C Trace Context + Baggage, `http.server.request.duration`, flush on
 * shutdown. No endpoint (or no SDK installed) = everything below is a no-op.
 *
 * PHP-FPM workers are long-lived processes that serve many requests, so metrics are cumulative per worker
 * (`service.instance.id` = host:pid) and exported at most every `metric_interval` seconds, after the response
 * has been sent. Spans are exported in batches at the end of the request.
 */
final class Telemetry
{
    private ?TracerProvider $tracers = null;

    private ?MeterProviderInterface $meters = null;

    private ?TextMapPropagatorInterface $propagator = null;

    private ?HistogramInterface $httpDuration = null;

    private ?ExportingReader $reader = null;

    private float $lastMetricExport = 0.0;

    /** @var array<string, CounterInterface> */
    private array $counters = [];

    private function __construct(private readonly int $metricInterval) {}

    public static function disabled(): self
    {
        return new self(30);
    }

    /** @param array<string, mixed> $cfg the "telemetry" part of config/laravelkit.php */
    public static function fromConfig(array $cfg): self
    {
        $endpoint = (string) ($cfg['endpoint'] ?? '');
        if (! ($cfg['enabled'] ?? true) || $endpoint === '' || ! class_exists(TracerProvider::class) || ! class_exists(SpanExporter::class)) {
            return self::disabled();
        }
        $base = self::httpEndpoint($endpoint, (string) ($cfg['protocol'] ?? 'http/protobuf'));
        $factory = new OtlpHttpTransportFactory;
        $ct = 'application/x-protobuf';
        $spans = new SpanExporter($factory->create($base.'/v1/traces', $ct, [], null, 3.0, 100, 1));
        $metrics = new MetricExporter($factory->create($base.'/v1/metrics', $ct, [], null, 3.0, 100, 1), 'Cumulative');

        return self::build($spans, $metrics, (string) ($cfg['service_name'] ?? 'laravel'), (int) ($cfg['metric_interval'] ?? 30), batch: true);
    }

    /**
     * The infra hands out the gRPC endpoint (`http://otel-collector:4317`, protocol `grpc`). PHP has no gRPC
     * client without an extension, so the collector's OTLP/HTTP receiver (port 4318) is used instead.
     */
    public static function httpEndpoint(string $endpoint, string $protocol): string
    {
        $endpoint = rtrim($endpoint, '/');
        if ($protocol === 'grpc' || str_ends_with($endpoint, ':4317')) {
            $endpoint = preg_replace('/:4317$/', ':4318', $endpoint) ?? $endpoint;
        }

        return $endpoint;
    }

    public static function build(SpanExporterInterface $spans, MetricExporterInterface $metrics, string $service, int $metricInterval = 30, bool $batch = false): self
    {
        $t = new self($metricInterval);
        $resource = ResourceInfoFactory::defaultResource()->merge(ResourceInfo::create(Attributes::create([
            ResourceAttributes::SERVICE_NAME => $service,
            ResourceAttributes::SERVICE_INSTANCE_ID => (gethostname() ?: 'host').':'.getmypid(),
        ])));
        $processor = $batch
            ? new BatchSpanProcessor($spans, Clock::getDefault(), 512, 5000, 1024)
            : new SimpleSpanProcessor($spans);
        $t->tracers = new TracerProvider($processor, new ParentBased(new AlwaysOnSampler), $resource);
        $t->reader = new ExportingReader($metrics);
        $t->meters = MeterProvider::builder()->setResource($resource)->addReader($t->reader)->build();
        $t->httpDuration = $t->meters->getMeter('pourydev/laravelkit')->createHistogram(
            'http.server.request.duration', 's', 'Duration of HTTP server requests.',
            ['ExplicitBucketBoundaries' => [0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10]],
        );
        $t->propagator = new MultiTextMapPropagator([TraceContextPropagator::getInstance(), BaggagePropagator::getInstance()]);

        return $t;
    }

    public function enabled(): bool
    {
        return $this->tracers !== null;
    }

    public function tracer(): ?TracerInterface
    {
        return $this->tracers?->getTracer('pourydev/laravelkit');
    }

    public function propagator(): ?TextMapPropagatorInterface
    {
        return $this->propagator;
    }

    /** Bounded-label counter (never per-user / per-recipient labels). */
    public function count(string $name, array $attributes = [], int $by = 1): void
    {
        if ($this->meters === null) {
            return;
        }
        ($this->counters[$name] ??= $this->meters->getMeter('pourydev/laravelkit')->createCounter($name))->add($by, $attributes);
    }

    public function recordHttpRequest(float $seconds, array $attributes): void
    {
        $this->httpDuration?->record($seconds, $attributes);
    }

    /** Called after the response was sent: exports spans of this request and (rate limited) the metrics. */
    public function flush(): void
    {
        if (! $this->enabled()) {
            return;
        }
        try {
            $this->tracers->forceFlush();
            $now = microtime(true);
            if ($this->reader !== null && $now - $this->lastMetricExport >= $this->metricInterval) {
                $this->lastMetricExport = $now;
                $this->reader->collect();
            }
        } catch (\Throwable $e) {
            error_log('laravelkit telemetry flush failed: '.$e->getMessage()); // telemetry must never fail a request
        }
    }

    /** On worker shutdown: final export of everything. */
    public function shutdown(): void
    {
        if (! $this->enabled()) {
            return;
        }
        try {
            $this->reader?->collect();
            $this->meters?->shutdown();
            $this->tracers->shutdown();
        } catch (\Throwable) {
        }
    }
}
