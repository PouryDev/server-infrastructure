<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Integration;

use Illuminate\Support\Facades\Route;
use PouryDev\LaravelKit\Telemetry\Telemetry;
use PouryDev\LaravelKit\Tests\TestCase;

/**
 * The real OTLP exporters against a stand-in collector (php -S). Proves what the unit tests cannot: that the
 * infra's gRPC endpoint (`http://otel-collector:4317`, protocol `grpc`) ends up as protobuf POSTs to 4318.
 */
final class OtlpTest extends TestCase
{
    /** @var resource|null */
    private $server = null;

    private string $log;

    protected function defineRoutes($router): void
    {
        Route::get('api/hello', fn () => ['ok' => true]);
    }

    protected function setUp(): void
    {
        $this->log = sys_get_temp_dir().'/lk-collector-'.bin2hex(random_bytes(4)).'.log';
        $this->server = proc_open(
            [PHP_BINARY, '-S', '127.0.0.1:4318', __DIR__.'/stub/collector.php'],
            [1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']], $pipes, null, ['LK_COLLECTOR_LOG' => $this->log],
        );
        usleep(400_000);
        parent::setUp();
    }

    protected function tearDown(): void
    {
        if (is_resource($this->server)) {
            proc_terminate($this->server);
            proc_close($this->server);
        }
        foreach (glob($this->log.'*') ?: [] as $f) {
            @unlink($f);
        }
        parent::tearDown();
    }

    protected function defineEnvironment($app): void
    {
        parent::defineEnvironment($app);
        $app['config']->set('laravelkit.telemetry', [
            'enabled' => true, 'endpoint' => 'http://127.0.0.1:4317', 'protocol' => 'grpc', // exactly what the infra hands out
            'service_name' => 'it-svc', 'metric_interval' => 0,
        ]);
        $app->singleton(Telemetry::class, fn () => Telemetry::fromConfig(config('laravelkit.telemetry')));
    }

    public function test_spans_and_metrics_reach_the_collector_as_otlp_protobuf_over_http(): void
    {
        $this->getJson('api/hello', ['traceparent' => '00-cccccccccccccccccccccccccccccccc-dddddddddddddddd-01'])->assertOk();
        app(Telemetry::class)->shutdown();

        $lines = is_file($this->log) ? file($this->log, FILE_IGNORE_NEW_LINES) : [];
        $paths = array_map(fn ($l) => explode(' ', $l)[0], $lines);
        $this->assertContains('/v1/traces', $paths, 'got: '.json_encode($lines));
        $this->assertContains('/v1/metrics', $paths, 'got: '.json_encode($lines));
        foreach ($lines as $l) {
            $this->assertSame('application/x-protobuf', explode(' ', $l)[1]);
        }
        $traces = (string) file_get_contents($this->log.'.last_v1_traces');
        $this->assertStringContainsString(hex2bin('cccccccccccccccccccccccccccccccc'), $traces, 'the incoming trace id is in the exported span');
        $this->assertStringContainsString('GET /api/hello', $traces);
        $this->assertStringContainsString('it-svc', $traces, 'service.name resource attribute');
        $this->assertStringContainsString('http.server.request.duration', (string) file_get_contents($this->log.'.last_v1_metrics'));
    }

    public function test_an_unreachable_collector_never_fails_the_request(): void
    {
        if (is_resource($this->server)) {
            proc_terminate($this->server);
            proc_close($this->server);
            $this->server = null;
            usleep(200_000);
        }
        $this->getJson('api/hello')->assertOk();
        app(Telemetry::class)->flush();
        $this->addToAssertionCount(1);
    }
}
