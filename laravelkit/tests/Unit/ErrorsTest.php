<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Unit;

use Illuminate\Contracts\Debug\ExceptionHandler;
use Illuminate\Foundation\Exceptions\Handler;
use Illuminate\Support\Facades\Route;
use Illuminate\Support\Facades\Validator;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Errors\Kind;
use PouryDev\LaravelKit\LaravelKitServiceProvider;
use PouryDev\LaravelKit\Tests\TestCase;

final class ErrorsTest extends TestCase
{
    protected function defineRoutes($router): void
    {
        Route::get('api/invalid', fn () => throw AppException::invalid('bad', ['email' => 'is required']));
        Route::get('api/notfound', fn () => throw AppException::notFound('User'));
        Route::get('api/conflict', fn () => throw AppException::conflict('already exists'));
        Route::get('api/internal', fn () => throw AppException::make(Kind::Internal, 'boom', 'secret detail', new \RuntimeException('db password=hunter2')));
        Route::get('api/unavailable', fn () => throw AppException::unavailable('try later', 'revocation_check_failed'));
        Route::get('api/crash', fn () => throw new \RuntimeException('secret detail'));
        Route::get('api/validate', fn () => Validator::make([], ['name' => 'required'])->validate());
        Route::get('web/page', fn () => throw AppException::notFound('Page'));
    }

    public function test_app_exception_uses_the_uniform_body_and_status(): void
    {
        $r = $this->getJson('api/invalid');
        $r->assertStatus(400)->assertJson(['error' => ['code' => 'invalid_argument', 'message' => 'bad', 'fields' => [['field' => 'email', 'message' => 'is required']]]]);
        $this->assertStringContainsString('no-store', (string) $r->headers->get('Cache-Control'));
        $this->getJson('api/notfound')->assertStatus(404)->assertJsonPath('error.code', 'not_found')->assertJsonPath('error.message', 'User not found');
        $this->getJson('api/conflict')->assertStatus(409);
    }

    public function test_server_errors_never_leak_internals(): void
    {
        foreach (['api/internal', 'api/crash'] as $p) {
            $r = $this->getJson($p)->assertStatus(500);
            $this->assertSame(['code' => 'internal', 'message' => 'internal error'], array_intersect_key($r->json('error'), array_flip(['code', 'message'])));
            $this->assertStringNotContainsString('hunter2', $r->getContent());
            $this->assertStringNotContainsString('secret detail', $r->getContent());
        }
    }

    public function test_unavailable_keeps_its_code_and_asks_to_retry(): void
    {
        $this->getJson('api/unavailable')->assertStatus(503)->assertHeader('Retry-After', '1')->assertJsonPath('error.code', 'revocation_check_failed');
    }

    public function test_laravel_validation_becomes_field_errors(): void
    {
        $this->getJson('api/validate')->assertStatus(400)->assertJsonPath('error.code', 'invalid_argument')->assertJsonPath('error.fields.0.field', 'name');
    }

    public function test_unknown_route_is_a_json_404_without_internals(): void
    {
        $this->getJson('api/nothing-here')->assertStatus(404)->assertJsonPath('error.code', 'not_found')->assertJsonPath('error.message', 'not found');
    }

    public function test_non_api_html_requests_keep_laravels_own_rendering(): void
    {
        $this->get('web/page')->assertStatus(500); // AppException is not an HTTP exception; Laravel renders its page, we do not touch non-API requests
    }

    public function test_the_hook_finds_the_framework_handler_behind_a_wrapper_like_collisions(): void
    {
        $real = $this->app->make(ExceptionHandler::class);
        $wrapper = new class($real)
        {
            public function __construct(protected $appExceptionHandler) {}
        };
        $this->assertSame($real, LaravelKitServiceProvider::unwrapHandler($wrapper));
        $this->assertSame($real, LaravelKitServiceProvider::unwrapHandler($real));
        $this->assertNull(LaravelKitServiceProvider::unwrapHandler(new \stdClass));
    }

    public function test_errors_can_be_switched_off_for_projects_with_their_own_error_contract(): void
    {
        $this->app['config']->set('laravelkit.errors.enabled', false);
        $handler = $this->app->make(ExceptionHandler::class);
        $fresh = new Handler($this->app);
        $this->app->instance(ExceptionHandler::class, $fresh);
        (new LaravelKitServiceProvider($this->app))->boot();
        $r = new \ReflectionProperty(Handler::class, 'renderCallbacks');
        $this->assertSame([], $r->getValue($fresh), 'no render hook registered when disabled');
    }

    public function test_kind_of_walks_the_previous_chain(): void
    {
        $this->assertSame(Kind::NotFound, AppException::kindOf(new \RuntimeException('x', 0, AppException::notFound())));
        $this->assertSame(Kind::Internal, AppException::kindOf(new \RuntimeException('x')));
    }
}
