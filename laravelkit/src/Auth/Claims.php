<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

/** The verified content of a token (same claim names as gokit: typ, roles, ext, jti). */
final class Claims
{
    /** @param list<string> $roles @param array<string, mixed> $extra */
    public function __construct(
        public readonly string $subject,
        public readonly array $roles,
        public readonly array $extra,
        public readonly TokenType $type,
        public readonly string $id,
        public readonly int $issuedAt,
        public readonly int $expiresAt,
    ) {}

    public function hasAnyRole(string ...$roles): bool
    {
        return array_intersect($roles, $this->roles) !== [];
    }
}
