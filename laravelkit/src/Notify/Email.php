<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

use PouryDev\LaravelKit\Errors\AppException;

/** An email. Rendering templates belongs to the project; this carries the finished subject and bodies. */
final class Email
{
    /** @param list<string> $to @param list<string> $cc @param string $id stable across retries, for de-duplication downstream */
    public function __construct(
        public readonly array $to,
        public readonly string $subject,
        public readonly string $text = '',
        public readonly string $html = '',
        public readonly array $cc = [],
        public readonly string $id = '',
    ) {}

    public function withId(): self
    {
        return $this->id !== '' ? $this : new self($this->to, $this->subject, $this->text, $this->html, $this->cc, bin2hex(random_bytes(12)));
    }

    /** @throws AppException invalid, with field errors (header injection, bad address, empty body) */
    public function validate(): void
    {
        $e = [];
        if ($this->to === []) {
            $e['to'] = 'at least one recipient is required';
        }
        foreach ([...$this->to, ...$this->cc] as $a) {
            if (! is_string($a) || filter_var($a, FILTER_VALIDATE_EMAIL) === false || preg_match('/[\r\n]/', $a)) {
                $e['to'] = 'invalid email address';
                break;
            }
        }
        if (preg_match('/[\r\n]/', $this->subject) || trim($this->subject) === '') {
            $e['subject'] = 'subject is required and must be a single line';
        }
        if (trim($this->text) === '' && trim($this->html) === '') {
            $e['body'] = 'text or html is required';
        }
        if ($e !== []) {
            throw AppException::invalid('invalid email', $e);
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
        return new self($a['to'] ?? [], $a['subject'] ?? '', $a['text'] ?? '', $a['html'] ?? '', $a['cc'] ?? [], $a['id'] ?? '');
    }
}
