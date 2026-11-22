<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Errors;

use RuntimeException;
use Throwable;

/**
 * A classified application error: a kind, a stable machine code, a client-safe message, field errors and a
 * hidden cause. Throw it from any layer; {@see ErrorRenderer} turns it into the uniform error body.
 */
class AppException extends RuntimeException
{
    /** @param list<array{field: string, message: string}> $fields */
    public function __construct(
        public readonly Kind $kind,
        public readonly string $errorCode,
        string $message,
        public readonly array $fields = [],
        ?Throwable $cause = null,
    ) {
        parent::__construct($message, 0, $cause);
    }

    public static function make(Kind $kind, string $code = '', string $message = '', ?Throwable $cause = null): self
    {
        return new self($kind, $code !== '' ? $code : $kind->value, $message, [], $cause);
    }

    /** @param array<string, string>|list<array{field: string, message: string}> $fields */
    public static function invalid(string $message = 'invalid input', array $fields = []): self
    {
        $list = [];
        foreach ($fields as $k => $v) {
            $list[] = is_array($v) ? $v : ['field' => (string) $k, 'message' => (string) $v];
        }

        return new self(Kind::Invalid, Kind::Invalid->value, $message, $list);
    }

    public static function invalidField(string $field, string $message): self
    {
        return self::invalid('invalid input', [$field => $message]);
    }

    public static function notFound(string $entity = 'resource'): self
    {
        return self::make(Kind::NotFound, '', $entity.' not found');
    }

    public static function conflict(string $message): self
    {
        return self::make(Kind::Conflict, '', $message);
    }

    public static function unauthorized(string $message = 'unauthorized', string $code = ''): self
    {
        return self::make(Kind::Unauthorized, $code, $message);
    }

    public static function forbidden(string $message = 'forbidden'): self
    {
        return self::make(Kind::Forbidden, '', $message);
    }

    public static function unavailable(string $message = 'service unavailable', string $code = '', ?Throwable $cause = null): self
    {
        return self::make(Kind::Unavailable, $code, $message, $cause);
    }

    public static function internal(?Throwable $cause = null): self
    {
        return self::make(Kind::Internal, '', 'internal error', $cause);
    }

    /** Kind of any throwable; unclassified ones are Internal. */
    public static function kindOf(Throwable $e): Kind
    {
        for ($t = $e; $t !== null; $t = $t->getPrevious()) {
            if ($t instanceof self) {
                return $t->kind;
            }
        }

        return Kind::Internal;
    }
}
