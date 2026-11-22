<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit;

use Illuminate\Contracts\Bus\Dispatcher;
use Illuminate\Contracts\Debug\ExceptionHandler;
use Illuminate\Contracts\Http\Kernel;
use Illuminate\Foundation\Exceptions\Handler;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Auth;
use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Route;
use Illuminate\Support\ServiceProvider;
use PouryDev\LaravelKit\Auth\Jwt;
use PouryDev\LaravelKit\Auth\JwtGuard;
use PouryDev\LaravelKit\Auth\KeySet;
use PouryDev\LaravelKit\Auth\PasswordHasher;
use PouryDev\LaravelKit\Auth\Revocations;
use PouryDev\LaravelKit\Cache\KitCache;
use PouryDev\LaravelKit\Console\InstallCommand;
use PouryDev\LaravelKit\Console\JwtKeyCommand;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Errors\ErrorRenderer;
use PouryDev\LaravelKit\Http\HealthController;
use PouryDev\LaravelKit\Http\Middleware\AccessLog;
use PouryDev\LaravelKit\Http\Middleware\Authenticate;
use PouryDev\LaravelKit\Http\Middleware\RequestId;
use PouryDev\LaravelKit\Http\Middleware\RequireRoles;
use PouryDev\LaravelKit\Http\Middleware\Trace;
use PouryDev\LaravelKit\Logging\JsonStderrChannel;
use PouryDev\LaravelKit\Notify\AsyncNotifier;
use PouryDev\LaravelKit\Notify\LogNotifier;
use PouryDev\LaravelKit\Notify\Notifier;
use PouryDev\LaravelKit\Notify\Providers\Kavenegar;
use PouryDev\LaravelKit\Notify\Providers\LogSms;
use PouryDev\LaravelKit\Notify\Providers\Twilio;
use PouryDev\LaravelKit\Notify\SmsSender;
use PouryDev\LaravelKit\Notify\SyncNotifier;
use PouryDev\LaravelKit\Telemetry\Instrumentation;
use PouryDev\LaravelKit\Telemetry\Telemetry;
use Throwable;

final class LaravelKitServiceProvider extends ServiceProvider
{
    public function register(): void
    {
        $this->mergeConfigFrom(__DIR__.'/../config/laravelkit.php', 'laravelkit');

        $this->app->singleton(Telemetry::class, fn () => Telemetry::fromConfig((array) config('laravelkit.telemetry')));

        $this->app->singleton(ErrorRenderer::class, fn () => new ErrorRenderer(
            (array) config('laravelkit.errors.paths'), (bool) config('laravelkit.errors.always_json'),
        ));

        // --- auth
        $this->app->singleton(KeySet::class, fn () => KeySet::fromConfig((array) config('laravelkit.auth')));
        $this->app->singleton(Revocations::class, function () {
            $store = config('laravelkit.auth.revocation_store');

            return new Revocations(Cache::store($store ?: null), (int) config('laravelkit.auth.refresh_ttl'));
        });
        $this->app->singleton(Jwt::class, function ($app) {
            $c = (array) config('laravelkit.auth');

            return new Jwt(
                $app->make(KeySet::class), (string) $c['issuer'], (string) $c['audience'],
                (int) $c['access_ttl'], (int) $c['refresh_ttl'], (int) $c['leeway'],
                ! empty($c['revocation_store']) ? $app->make(Revocations::class) : null,
            );
        });
        $this->app->singleton(PasswordHasher::class);

        // --- cache
        $this->app->singleton(KitCache::class, function ($app) {
            $c = (array) config('laravelkit.cache');

            return new KitCache(
                Cache::store($c['store'] ?: null), (string) $c['namespace'], (float) $c['jitter'], (int) $c['max_ttl'],
                (int) $c['version_ttl'], (float) $c['version_refresh'], $app->make('log')->channel(config('logging.default')),
            );
        });

        // --- http middleware parameters
        $this->app->when(RequestId::class)->needs('$header')->give(fn () => (string) config('laravelkit.http.request_id_header'));
        $this->app->when(AccessLog::class)->needs('$channel')->give(fn () => config('laravelkit.http.access_log_channel'));
        $this->app->when(AccessLog::class)->needs('$skip')->give(fn () => (array) config('laravelkit.http.skip_log_paths'));
        $this->app->when(Authenticate::class)->needs('$guard')->give(fn () => (string) config('laravelkit.auth.guard'));

        // --- notify
        $this->app->singleton(SmsSender::class, function () {
            $c = (array) config('laravelkit.notify.sms');

            return match ($c['provider'] ?? 'log') {
                'kavenegar' => new Kavenegar((string) $c['kavenegar']['api_key'], (string) ($c['kavenegar']['sender'] ?? '')),
                'twilio' => new Twilio((string) $c['twilio']['account_sid'], (string) $c['twilio']['auth_token'], (string) $c['twilio']['from']),
                'log' => new LogSms,
                default => throw new \InvalidArgumentException("unknown SMS_PROVIDER '{$c['provider']}'"),
            };
        });
        $this->app->singleton(SyncNotifier::class, fn ($app) => new SyncNotifier(
            $app->make(SmsSender::class), $app->make('log')->channel(config('logging.default')), $app->make(Telemetry::class),
            (int) config('laravelkit.notify.retries'), (int) config('laravelkit.notify.attempt_timeout'),
        ));
        $this->app->singleton(Notifier::class, function ($app) {
            return match ((string) config('laravelkit.notify.driver')) {
                'async' => new AsyncNotifier(
                    $app->make(Dispatcher::class), $app->make(Telemetry::class), (string) config('laravelkit.notify.queue'),
                    config('laravelkit.notify.connection') ?: null, (int) config('laravelkit.notify.retries'),
                ),
                'log' => new LogNotifier($app->make('log')->channel(config('logging.default'))),
                'sync' => $app->make(SyncNotifier::class),
                default => throw new \InvalidArgumentException('unknown NOTIFY_DRIVER'),
            };
        });
    }

    public function boot(): void
    {
        $this->loadViewsFrom(__DIR__.'/../resources/views', 'laravelkit');
        if ($this->app->runningInConsole()) {
            $this->publishes([__DIR__.'/../config/laravelkit.php' => config_path('laravelkit.php')], 'laravelkit-config');
            $this->commands([InstallCommand::class, JwtKeyCommand::class]);
        }

        $this->registerErrors();
        $this->registerAuth();
        $this->registerHttp();
        $this->registerLogging();
        $telemetry = $this->app->make(Telemetry::class);
        Instrumentation::register($this->app, $telemetry);
        if ($telemetry->enabled()) {
            register_shutdown_function(static fn () => $telemetry->shutdown()); // worker exit: final export
        }
    }

    private function registerErrors(): void
    {
        if (! config('laravelkit.errors.enabled', true)) {
            return;
        }
        $this->callAfterResolving(ExceptionHandler::class, function ($handler): void {
            $handler = self::unwrapHandler($handler);
            if ($handler === null) {
                return;
            }
            $handler->renderable(fn (Throwable $e, Request $request) => $this->app->make(ErrorRenderer::class)->render($e, $request));
            // client errors raised as AppException are expected outcomes, not incidents
            $handler->dontReportWhen(static fn (Throwable $e) => $e instanceof AppException && $e->kind->status() < 500);
        });
    }

    /**
     * The bound handler is not always the framework's: in development `nunomaduro/collision` (part of every
     * Laravel skeleton) wraps it and delegates `render()` / `report()` to the original in `$appExceptionHandler`.
     * The kit's hooks belong on the original, which is the one that does the rendering.
     */
    public static function unwrapHandler(object $handler): ?Handler
    {
        for ($i = 0; $i < 5 && ! $handler instanceof Handler; $i++) {
            $ref = new \ReflectionObject($handler);
            if (! $ref->hasProperty('appExceptionHandler')) {
                return null;
            }
            $handler = $ref->getProperty('appExceptionHandler')->getValue($handler);
        }

        return $handler instanceof Handler ? $handler : null;
    }

    private function registerAuth(): void
    {
        Auth::extend('jwt', function ($app, string $name, array $config) {
            $provider = ! empty($config['provider']) ? Auth::createUserProvider($config['provider']) : null;

            $guard = new JwtGuard($app->make(Jwt::class), $app->make('request'), $provider);
            $app->refresh('request', $guard, 'setRequest');

            return $guard;
        });
        $guard = (string) config('laravelkit.auth.guard');
        if (config("auth.guards.$guard") === null) {
            config(["auth.guards.$guard" => ['driver' => 'jwt', 'provider' => null]]);
        }
        $router = $this->app['router'];
        $router->aliasMiddleware('kit.auth', Authenticate::class);
        $router->aliasMiddleware('kit.roles', RequireRoles::class);
    }

    private function registerHttp(): void
    {
        $router = $this->app['router'];
        $router->aliasMiddleware('kit.request', RequestId::class);
        $router->aliasMiddleware('kit.trace', Trace::class);
        $router->aliasMiddleware('kit.log', AccessLog::class);
        // one group for projects that prefer to opt in per route group instead of globally
        $router->middlewareGroup('kit', [RequestId::class, Trace::class, AccessLog::class]);

        if (config('laravelkit.http.global_middleware', true) && $this->app->bound(Kernel::class)) {
            $kernel = $this->app->make(Kernel::class);
            foreach ([RequestId::class, Trace::class] as $m) {
                $kernel->prependMiddleware($m);
            }
            if (config('laravelkit.http.access_log')) {
                $kernel->pushMiddleware(AccessLog::class);
            }
        }
        if (config('laravelkit.http.health_routes') && ! $this->app->routesAreCached()) {
            Route::get('healthz', [HealthController::class, 'live'])->name('laravelkit.healthz');
            Route::get('readyz', [HealthController::class, 'ready'])->name('laravelkit.readyz');
        }
    }

    private function registerLogging(): void
    {
        if (config('logging.channels.kit') === null) {
            config(['logging.channels.kit' => ['driver' => 'custom', 'via' => JsonStderrChannel::class, 'level' => config('logging.channels.stderr.level', 'debug')]]);
        }
    }
}
