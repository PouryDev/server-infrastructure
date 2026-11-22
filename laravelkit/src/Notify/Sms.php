<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

use PouryDev\LaravelKit\Errors\AppException;

final class Sms
{
    public function __construct(public readonly string $to, public readonly string $body, public readonly string $id = '') {}

    public function withId(): self
    {
        return $this->id !== '' ? $this : new self($this->to, $this->body, bin2hex(random_bytes(12)));
    }

    /** Accepts Iranian local (09123456789) and international (+98..., +1...) numbers. */
    public function validate(): void
    {
        $e = [];
        if (preg_match('/^(\+\d{8,15}|0\d{9,12}|00\d{8,15})$/', $this->to) !== 1) {
            $e['to'] = 'invalid phone number';
        }
        if (trim($this->body) === '' || mb_strlen($this->body) > 1600) {
            $e['body'] = 'body is required and at most 1600 characters';
        }
        if ($e !== []) {
            throw AppException::invalid('invalid sms', $e);
        }
    }

    /** @return array<string, mixed> */
    public function toArray(): array
    {
        return get_object_vars($this);
    }

    /** @param array<string, mixed> $a */
    public static function fromArray(array $a): self
    {
        return new self($a['to'] ?? '', $a['body'] ?? '', $a['id'] ?? '');
    }
}
