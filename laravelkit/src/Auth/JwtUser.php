<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

use Illuminate\Contracts\Auth\Authenticatable;

/** The identity a verified token stands for when the guard has no user provider (nothing hits the database). */
final class JwtUser implements Authenticatable
{
    public function __construct(public readonly Claims $claims) {}

    public function id(): string
    {
        return $this->claims->subject;
    }

    /** @return list<string> */
    public function roles(): array
    {
        return $this->claims->roles;
    }

    public function hasAnyRole(string ...$roles): bool
    {
        return $this->claims->hasAnyRole(...$roles);
    }

    public function getAuthIdentifierName(): string
    {
        return 'id';
    }

    public function getAuthIdentifier(): string
    {
        return $this->claims->subject;
    }

    public function getAuthPasswordName(): string
    {
        return 'password';
    }

    public function getAuthPassword(): string
    {
        return '';
    }

    public function getRememberToken(): ?string
    {
        return null;
    }

    public function setRememberToken($value): void {}

    public function getRememberTokenName(): string
    {
        return '';
    }
}
