<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Http\Middleware;

use Closure;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Log;
use Symfony\Component\HttpFoundation\Response;

/**
 * Gives every request an id (a sane incoming X-Request-Id is kept), echoes it in the response, shares it with
 * every log line and makes API responses `no-store` unless the handler chose a Cache-Control itself (the CDN
 * in front must never cache API data by accident).
 */
final class RequestId
{
    public const ATTRIBUTE = 'laravelkit.request_id';

    public function __construct(private readonly string $header = 'X-Request-Id') {}

    public function handle(Request $request, Closure $next): Response
    {
        $in = (string) $request->headers->get($this->header, '');
        $id = preg_match('/^[A-Za-z0-9._-]{8,64}$/', $in) === 1 ? $in : bin2hex(random_bytes(8));
        $request->attributes->set(self::ATTRIBUTE, $id);
        Log::shareContext(['request_id' => $id]);

        $response = $next($request);
        $response->headers->set($this->header, $id);
        // Symfony fills in "no-cache, private" when nobody chose a Cache-Control: that is the case to override
        $cc = (string) $response->headers->get('Cache-Control', '');
        if ($cc === '' || $cc === 'no-cache, private') {
            $response->headers->set('Cache-Control', 'no-store');
        }

        return $response;
    }
}
