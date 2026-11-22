<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Http\Middleware;

use Closure;
use Illuminate\Http\Request;
use OpenTelemetry\API\Trace\SpanInterface;
use OpenTelemetry\API\Trace\SpanKind;
use OpenTelemetry\API\Trace\StatusCode;
use OpenTelemetry\Context\ScopeInterface;
use PouryDev\LaravelKit\Telemetry\HeaderGetter;
use PouryDev\LaravelKit\Telemetry\Telemetry;
use Symfony\Component\HttpFoundation\Response;

/**
 * Server span per request, continued from the `traceparent` nginx forwards, plus the semantic-convention
 * histogram `http.server.request.duration` (bounded labels: method, route template, status class).
 */
final class Trace
{
    public const SPAN = 'laravelkit.span';

    public const SCOPE = 'laravelkit.scope';

    public const START = 'laravelkit.start';

    public function __construct(private readonly Telemetry $telemetry) {}

    public function handle(Request $request, Closure $next): Response
    {
        $tracer = $this->telemetry->tracer();
        if ($tracer === null) {
            return $next($request);
        }
        $request->attributes->set(self::START, hrtime(true));
        $parent = $this->telemetry->propagator()->extract($request->headers->all(), new HeaderGetter);
        $span = $tracer->spanBuilder($request->method())->setParent($parent)->setSpanKind(SpanKind::KIND_SERVER)
            ->setAttribute('http.request.method', $request->method())
            ->setAttribute('url.path', $request->getPathInfo())
            ->setAttribute('url.scheme', $request->getScheme())
            ->setAttribute('server.address', $request->getHost())
            ->setAttribute('user_agent.original', substr((string) $request->userAgent(), 0, 256))
            ->startSpan();
        $request->attributes->set(self::SPAN, $span);
        $request->attributes->set(self::SCOPE, $span->activate());

        try {
            return $next($request);
        } catch (\Throwable $e) {
            $span->recordException($e);
            $span->setStatus(StatusCode::STATUS_ERROR, $e::class);
            throw $e;
        }
    }

    /** Runs in `terminate()`, i.e. after the response went out. */
    public function terminate(Request $request, Response $response): void
    {
        $span = $request->attributes->get(self::SPAN);
        if (! $span instanceof SpanInterface) {
            return;
        }
        $route = $request->route()?->uri();
        $route = $route !== null ? '/'.ltrim($route, '/') : 'unmatched';
        $status = $response->getStatusCode();
        $span->updateName($request->method().' '.$route);
        $span->setAttribute('http.route', $route);
        $span->setAttribute('http.response.status_code', $status);
        if ($status >= 500) {
            $span->setStatus(StatusCode::STATUS_ERROR);
        }
        $scope = $request->attributes->get(self::SCOPE);
        if ($scope instanceof ScopeInterface) {
            $scope->detach();
        }
        $span->end();

        $start = $request->attributes->get(self::START);
        $attrs = ['http.request.method' => $request->method(), 'http.route' => $route, 'http.response.status_code' => $status];
        if ($status >= 500) {
            $attrs['error.type'] = (string) $status;
        }
        $this->telemetry->recordHttpRequest((hrtime(true) - (int) $start) / 1e9, $attrs);
        $this->telemetry->flush();
    }
}
