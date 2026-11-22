<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

use Illuminate\Contracts\Cache\Repository;

/**
 * Makes stateless tokens revocable with two kinds of short-lived keys in the (shared Redis) cache store:
 *
 *   auth:revoked:<jti>          one revoked token, kept until the token would have expired anyway
 *   auth:revoked-before:<sub>   "everything issued to <sub> up to this instant is void"
 *
 * Every key has a TTL, so the set never outgrows the tokens that could still be presented. A check is one
 * MGET. If the store is down the request fails closed (503): see Jwt::verify().
 */
final class Revocations
{
    public function __construct(
        private readonly Repository $store,
        private readonly int $maxTokenLifetime = 2_592_000,
        private readonly ?\Closure $clock = null,
    ) {}

    public function revoke(Claims $c): void
    {
        if ($c->id === '') {
            throw new \InvalidArgumentException('cannot revoke a token without an id');
        }
        $ttl = $c->expiresAt > 0 ? $c->expiresAt - $this->now() + 60 : $this->maxTokenLifetime;
        if ($ttl > 0) {
            $this->store->put('auth:revoked:'.$c->id, 1, $ttl);
        }
    }

    /** "Log out everywhere": voids every token of `$subject` issued up to now. */
    public function revokeSubject(string $subject): void
    {
        $this->store->put('auth:revoked-before:'.$subject, $this->now(), $this->maxTokenLifetime);
    }

    public function isRevoked(Claims $c): bool
    {
        $v = $this->store->many(['auth:revoked:'.$c->id, 'auth:revoked-before:'.$c->subject]);
        if ($v['auth:revoked:'.$c->id] !== null) {
            return true;
        }
        $before = $v['auth:revoked-before:'.$c->subject];

        return $before !== null && $c->issuedAt <= (int) $before;
    }

    private function now(): int
    {
        return $this->clock ? (int) ($this->clock)() : time();
    }
}
