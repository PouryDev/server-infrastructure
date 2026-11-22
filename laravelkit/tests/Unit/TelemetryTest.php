<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Unit;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Route;
use Monolog\Level;
use Monolog\LogRecord;
use OpenTelemetry\API\Trace\Span;
use OpenTelemetry\SDK\Metrics\MetricExporter\InMemoryExporter as MetricMemory;
use OpenTelemetry\SDK\Trace\SpanExporter\InMemoryExporter as SpanMemory;
use PouryDev\LaravelKit\Logging\TraceContextProcessor;
use PouryDev\LaravelKit\Telemetry\Telemetry;
use PouryDev\LaravelKit\Tests\TestCase;

final class TelemetryTest extends TestCase
{
    private SpanMemory $spans;

    private MetricMemory $metrics;

    protected function defineEnvironment($app): void
    {
        parent::defineEnvironment($app);
        $this->spans = new SpanMemory;
        $this->metrics = new MetricMemory;
        $app->singleton(Telemetry::class, fn () => Telemetry::build($this->spans, $this->metrics, 'test-svc', 0));
    }

    protected function defineRoutes($router): void
    {
        Route::get('api/things/{id}', function ($id) {
            DB::select('select 1');

            return ['trace' => Span::getCurrent()->getContext()->getTraceId()];
        });
        Route::get('api/boom', fn () => throw new \RuntimeException('x'));
    }

    public function test_server_span_continues_the_incoming_trace_and_uses_the_route_template(): void
    {
        $tid = 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa';
        $r = $this->getJson('api/things/42?secret=1', ['traceparent' => "00-$tid-bbbbbbbbbbbbbbbb-01"])->assertOk();
        $this->assertSame($tid, $r->json('trace'), 'the handler runs inside the continued trace');

        $names = array_map(fn ($s) => $s->getName(), $this->spans->getSpans());
        $this->assertContains('GET /api/things/{id}', $names);
        $server = array_values(array_filter($this->spans->getSpans(), fn ($s) => $s->getName() === 'GET /api/things/{id}'))[0];
        $this->assertSame($tid, $server->getTraceId());
        $this->assertSame('bbbbbbbbbbbbbbbb', $server->getParentSpanId());
        $a = $server->getAttributes()->toArray();
        $this->assertSame(200, $a['http.response.status_code']);
        $this->assertSame('/api/things/{id}', $a['http.route']);
        $this->assertSame('/api/things/42', $a['url.path'], 'no query string (it may carry secrets)');
        $db = array_values(array_filter($this->spans->getSpans(), fn ($s) => str_starts_with($s->getName(), 'SELECT')));
        $this->assertNotEmpty($db, 'database queries become child spans');
        $this->assertSame($tid, $db[0]->getTraceId());
    }

    public function test_a_request_without_traceparent_starts_a_new_trace_always_sampled(): void
    {
        $this->getJson('api/things/1')->assertOk();
        $this->assertNotEmpty($this->spans->getSpans());
        $this->assertTrue($this->spans->getSpans()[0]->getContext()->isSampled());
    }

    public function test_a_5xx_marks_the_span_as_error(): void
    {
        $this->getJson('api/boom')->assertStatus(500);
        $span = array_values(array_filter($this->spans->getSpans(), fn ($s) => $s->getName() === 'GET /api/boom'))[0];
        $this->assertSame('Error', $span->getStatus()->getCode());
    }

    public function test_the_semconv_duration_histogram_is_exported_with_bounded_labels(): void
    {
        $this->getJson('api/things/7');
        $this->getJson('api/things/8');
        app(Telemetry::class)->flush();
        $best = 0;
        $attrs = [];
        foreach ($this->metrics->collect(false) as $m) { // one batch per export; the in-memory exporter is delta, so counts add up
            if ($m->name !== 'http.server.request.duration') {
                continue;
            }
            $points = iterator_to_array($m->data->dataPoints);
            $this->assertLessThanOrEqual(1, count($points), 'one series: the route template, not the raw URL');
            if ($points !== []) {
                $best += $points[0]->count;
                $attrs = $points[0]->attributes->toArray();
            }
        }
        $this->assertSame(2, $best);
        $this->assertSame('/api/things/{id}', $attrs['http.route']);
        $keys = array_keys($attrs);
        sort($keys);
        $this->assertSame(['http.request.method', 'http.response.status_code', 'http.route'], $keys, 'bounded labels only');
    }

    public function test_log_lines_carry_the_active_trace_id(): void
    {
        $tracer = app(Telemetry::class)->tracer();
        $span = $tracer->spanBuilder('x')->startSpan();
        $scope = $span->activate();
        $rec = (new TraceContextProcessor)(new LogRecord(new \DateTimeImmutable, 'app', Level::Info, 'hi'));
        $scope->detach();
        $span->end();
        $this->assertSame($span->getContext()->getTraceId(), $rec->extra['trace_id']);
        $plain = (new TraceContextProcessor)(new LogRecord(new \DateTimeImmutable, 'app', Level::Info, 'hi'));
        $this->assertArrayNotHasKey('trace_id', $plain->extra);
    }

    public function test_the_infra_grpc_endpoint_is_mapped_to_the_collectors_http_receiver(): void
    {
        $this->assertSame('http://otel-collector:4318', Telemetry::httpEndpoint('http://otel-collector:4317', 'grpc'));
        $this->assertSame('http://otel-collector:4318', Telemetry::httpEndpoint('http://otel-collector:4317/', 'http/protobuf'));
        $this->assertSame('http://c:4318', Telemetry::httpEndpoint('http://c:4318', 'http/protobuf'));
    }

    public function test_no_endpoint_means_disabled_and_harmless(): void
    {
        $t = Telemetry::fromConfig(['enabled' => true, 'endpoint' => null]);
        $this->assertFalse($t->enabled());
        $t->flush();
        $t->count('x');
        $t->shutdown();
        $this->addToAssertionCount(1);
    }
}
