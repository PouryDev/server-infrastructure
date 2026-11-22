<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

use Illuminate\Auth\GuardHelpers;
use Illuminate\Contracts\Auth\Authenticatable;
use Illuminate\Contracts\Auth\Guard;
use Illuminate\Contracts\Auth\UserProvider;
use Illuminate\Http\Request;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Errors\Kind;

/**
 * Stateless guard: the bearer token is the whole session. Without a user provider `user()` is a
 * {@see JwtUser} built from the claims (no database); with one, the user is loaded by the token's subject.
 * A store outage while checking revocation is not "unauthenticated": it propagates as 503 (fail closed).
 */
final class JwtGuard implements Guard
{
    use GuardHelpers;

    public const CLAIMS_ATTRIBUTE = 'laravelkit.claims';

    public function __construct(
        private readonly Jwt $jwt,
        private Request $request,
        ?UserProvider $provider = null,
    ) {
        $this->provider = $provider;
    }

    /** Called when the container swaps the request (tests, Octane): never keep a user across requests. */
    public function setRequest(Request $request): self
    {
        $this->request = $request;
        $this->user = null;

        return $this;
    }

    public function user(): ?Authenticatable
    {
        if ($this->user !== null) {
            return $this->user;
        }
        try {
            $claims = $this->claims();
        } catch (AppException $e) {
            if ($e->kind === Kind::Unavailable) {
                throw $e;
            }

            return null;
        }
        if ($claims === null) {
            return null;
        }

        return $this->user = $this->provider !== null
            ? $this->provider->retrieveById($claims->subject)
            : new JwtUser($claims);
    }

    /** Verified claims of this request; null when there is no bearer token. Throws a classified error otherwise. */
    public function claims(): ?Claims
    {
        $cached = $this->request->attributes->get(self::CLAIMS_ATTRIBUTE);
        if ($cached instanceof Claims) {
            return $cached;
        }
        $token = $this->request->bearerToken();
        if ($token === null || $token === '') {
            return null;
        }
        $claims = $this->jwt->verify($token, TokenType::Access);
        $this->request->attributes->set(self::CLAIMS_ATTRIBUTE, $claims);

        return $claims;
    }

    public function validate(array $credentials = []): bool
    {
        return false; // credentials are checked by the login use case, not by the guard
    }
}
