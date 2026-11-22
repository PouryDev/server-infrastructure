<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Http\Middleware;

use Closure;
use Illuminate\Http\Request;
use PouryDev\LaravelKit\Auth\Claims;
use PouryDev\LaravelKit\Auth\JwtGuard;
use PouryDev\LaravelKit\Errors\AppException;
use Symfony\Component\HttpFoundation\Response;

/** `kit.roles:admin,editor`: the token must carry at least one of the roles (run after `kit.auth`). */
final class RequireRoles
{
    public function handle(Request $request, Closure $next, string ...$roles): Response
    {
        $claims = $request->attributes->get(JwtGuard::CLAIMS_ATTRIBUTE);
        if (! $claims instanceof Claims) {
            throw AppException::unauthorized('missing bearer token', 'missing_token');
        }
        if (! $claims->hasAnyRole(...$roles)) {
            throw AppException::forbidden('insufficient role');
        }

        return $next($request);
    }
}
