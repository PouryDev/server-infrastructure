<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Errors;

/** Classifies an error independently of the transport; the renderer maps it to an HTTP status. */
enum Kind: string
{
    case Internal = 'internal';
    case Invalid = 'invalid_argument';
    case Unauthorized = 'unauthorized';
    case Forbidden = 'forbidden';
    case NotFound = 'not_found';
    case Conflict = 'conflict';
    case Unavailable = 'unavailable';

    public function status(): int
    {
        return match ($this) {
            self::Invalid => 400,
            self::Unauthorized => 401,
            self::Forbidden => 403,
            self::NotFound => 404,
            self::Conflict => 409,
            self::Unavailable => 503,
            self::Internal => 500,
        };
    }
}
