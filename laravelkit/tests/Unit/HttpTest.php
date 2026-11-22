<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Unit;

use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Route;
use Monolog\Handler\TestHandler;
use Monolog\Logger;
use PouryDev\LaravelKit\Tests\TestCase;

final class HttpTest extends TestCase
{
    protected function defineRoutes($router): void
    {
        Route::get('api/ping', fn () => ['pong' => true]);
        Route::get('api/cached', fn () => response(['x' => 1])->header('Cache-Control', 'public, max-age=60'));
        Route::get('api/items/{id}', fn ($id) => ['id' => $id]);
    }

    public function test_every_response_gets_a_request_id_and_no_store(): void
    {
        $r = $this->getJson('api/ping')->assertOk();
        $this->assertMatchesRegularExpression('/^[a-f0-9]{16}$/', $r->headers->get('X-Request-Id'));
        $this->assertStringContainsString('no-store', $r->headers->get('Cache-Control'));
    }

    public function test_a_sane_incoming_request_id_is_kept_and_a_hostile_one_replaced(): void
    {
        $this->getJson('api/ping', ['X-Request-Id' => 'abc-12345678'])->assertHeader('X-Request-Id', 'abc-12345678');
        $r = $this->getJson('api/ping', ['X-Request-Id' => "x\r\ny: injected"]);
        $this->assertDoesNotMatchRegularExpression('/injected/', (string) $r->headers->get('X-Request-Id'));
    }

    public function test_a_handler_that_sets_cache_control_keeps_it(): void
    {
        $this->getJson('api/cached')->assertHeader('Cache-Control', 'max-age=60, public');
    }

    public function test_the_request_id_is_in_error_bodies(): void
    {
        $r = $this->getJson('api/nope');
        $this->assertSame($r->headers->get('X-Request-Id'), $r->json('error.request_id'));
    }

    public function test_healthz_is_always_ok_and_needs_nothing(): void
    {
        $this->app['config']->set('database.connections.testing.database', '/nonexistent/db.sqlite');
        DB::purge();
        $this->getJson('healthz')->assertOk()->assertExactJson(['status' => 'ok']);
    }

    public function test_readyz_reports_failed_dependencies_with_503(): void
    {
        $this->app['config']->set('laravelkit.http.ready_checks', ['database']);
        $this->getJson('readyz')->assertOk();
        $this->app['config']->set('database.connections.testing.database', '/nonexistent/dir/db.sqlite');
        DB::purge();
        $this->getJson('readyz')->assertStatus(503)->assertJson(['status' => 'unavailable', 'failed' => ['database']]);
    }

    public function test_access_log_line_has_route_template_status_and_no_query_string(): void
    {
        $h = new TestHandler;
        $this->app['config']->set('laravelkit.http.access_log_channel', 'capture');
        $this->app['config']->set('logging.channels.capture', ['driver' => 'capture']);
        $this->app['log']->extend('capture', fn () => new Logger('capture', [$h]));
        $this->getJson('api/items/42?token=secret')->assertOk();
        $this->getJson('healthz');
        $records = array_values(array_filter($h->getRecords(), fn ($r) => $r->message === 'request'));
        $this->assertCount(1, $records, 'health probes are not logged');
        $ctx = $records[0]->context;
        $this->assertSame(['GET', 'api/items/{id}', 200], [$ctx['method'], $ctx['route'], $ctx['status']]);
        $this->assertStringNotContainsString('secret', json_encode($ctx));
        $this->assertArrayHasKey('duration_ms', $ctx);
    }
}
