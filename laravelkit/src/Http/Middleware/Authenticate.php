<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Http\Middleware;

use Closure;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Auth;
use PouryDev\LaravelKit\Auth\JwtGuard;
use PouryDev\LaravelKit\Errors\AppException;
use Symfony\Component\HttpFoundation\Response;

/**
 * `kit.auth`: requires a valid access token and keeps the precise failure (token_expired tells the client to
 * refresh, token_revoked to log in again) instead of the generic 401 of `auth:jwt`. Claims are available as
 * `$request->attributes->get('laravelkit.claims')`.
 */
final class Authenticate
{
    public function __construct(private readonly string $guard = 'jwt') {}

    public function handle(Request $request, Closure $next): Response
    {
        $guard = Auth::guard($this->guard);
        if (! $guard instanceof JwtGuard) {
            throw new \LogicException("guard '{$this->guard}' is not a laravelkit JWT guard");
        }
        $claims = $guard->claims();
        if ($claims === null) {
            throw AppException::unauthorized('missing bearer token', 'missing_token');
        }
        Auth::shouldUse($this->guard);

        return $next($request);
    }
}
