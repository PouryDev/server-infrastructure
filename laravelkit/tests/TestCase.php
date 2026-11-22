<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests;

use Monolog\Handler\NullHandler;
use Orchestra\Testbench\TestCase as Base;
use PouryDev\LaravelKit\Auth\Key;
use PouryDev\LaravelKit\LaravelKitServiceProvider;

abstract class TestCase extends Base
{
    protected function getPackageProviders($app): array
    {
        return [LaravelKitServiceProvider::class];
    }

    protected function defineEnvironment($app): void
    {
        $app['config']->set('database.default', 'testing');
        $app['config']->set('database.connections.testing', ['driver' => 'sqlite', 'database' => ':memory:', 'prefix' => '']);
        $app['config']->set('cache.default', 'array');
        $app['config']->set('app.key', 'base64:'.base64_encode(random_bytes(32)));
        $app['config']->set('laravelkit.auth.private_key', Key::generateEd25519()['private_pem']);
        $app['config']->set('laravelkit.auth.issuer', 'test-issuer');
        $app['config']->set('laravelkit.auth.audience', 'test-aud');
        $app['config']->set('laravelkit.telemetry.endpoint', null);
        $app['config']->set('laravelkit.errors.paths', ['api/*']);
        $app['config']->set('logging.default', 'null');
        $app['config']->set('logging.channels.null', ['driver' => 'monolog', 'handler' => NullHandler::class]);
    }
}
