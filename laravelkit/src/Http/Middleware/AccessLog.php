<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Http\Middleware;

use Closure;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Log;
use PouryDev\LaravelKit\Auth\Claims;
use PouryDev\LaravelKit\Auth\JwtGuard;
use Psr\Log\LoggerInterface;
use Symfony\Component\HttpFoundation\Response;

/** One structured line per request (after the response is sent): route template, status, duration, trace_id. */
final class AccessLog
{
    private const START = 'laravelkit.access_start';

    /** @param list<string> $skip */
    public function __construct(private readonly ?string $channel = null, private readonly array $skip = ['healthz', 'readyz']) {}

    public function handle(Request $request, Closure $next): Response
    {
        $request->attributes->set(self::START, hrtime(true));

        return $next($request);
    }

    public function terminate(Request $request, Response $response): void
    {
        if ($request->is(...$this->skip)) {
            return;
        }
        $start = $request->attributes->get(self::START);
        $status = $response->getStatusCode();
        $claims = $request->attributes->get(JwtGuard::CLAIMS_ATTRIBUTE);
        $log = $this->channel ? Log::channel($this->channel) : Log::getFacadeRoot();
        /** @var LoggerInterface $log */
        $log->log($status >= 500 ? 'error' : 'info', 'request', array_filter([
            'method' => $request->method(),
            'route' => $request->route()?->uri() ?? $request->path(),
            'status' => $status,
            'duration_ms' => $start ? round((hrtime(true) - (int) $start) / 1e6, 2) : null,
            'bytes' => strlen((string) $response->getContent()) ?: null,
            'ip' => $request->ip(),
            'sub' => $claims instanceof Claims ? $claims->subject : null,
        ], static fn ($v) => $v !== null));
    }
}
