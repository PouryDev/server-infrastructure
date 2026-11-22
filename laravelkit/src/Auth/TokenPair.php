<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

use Illuminate\Contracts\Support\Arrayable;

final class TokenPair implements Arrayable
{
    public function __construct(
        public readonly string $accessToken,
        public readonly string $refreshToken,
        public readonly int $expiresIn,
        public readonly int $accessExpiresAt,
        public readonly int $refreshExpiresAt,
    ) {}

    public function toArray(): array
    {
        return ['access_token' => $this->accessToken, 'refresh_token' => $this->refreshToken,
            'token_type' => 'Bearer', 'expires_in' => $this->expiresIn];
    }
}
