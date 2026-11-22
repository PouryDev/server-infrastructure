<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Query;

use Illuminate\Http\Request;
use PouryDev\LaravelKit\Errors\AppException;

/**
 * Raw (syntax-checked, not yet validated) list parameters:
 *
 *   ?filter[role]=admin&filter[email][prefix]=ada&filter[created_at][between]=2026-01-01,2026-02-01
 *   &filter[id][in]=a,b&filter[deleted_at][null]=true&sort=-created_at,name&page=2&page_size=50
 *
 * Field names, operators and values are validated against a {@see Schema} when the query is applied.
 */
final class Params
{
    /** @param list<array{field: string, op: string, values: list<string>}> $filters
     *  @param list<array{field: string, desc: bool}> $sort */
    public function __construct(
        public readonly array $filters = [],
        public readonly array $sort = [],
        public readonly int $page = 0,
        public readonly int $pageSize = 0,
        public readonly ?string $cursor = null,
    ) {}

    public static function fromRequest(Request $request): self
    {
        return self::fromArray($request->query());
    }

    /** @param array<string, mixed> $q */
    public static function fromArray(array $q): self
    {
        $bad = [];
        $filters = [];
        $raw = $q['filter'] ?? [];
        if (! is_array($raw)) {
            throw AppException::invalid('invalid list parameters', ['filter' => 'expected filter[field]=value']);
        }
        foreach ($raw as $field => $spec) {
            $field = (string) $field;
            if (! is_array($spec)) { // filter[field]=v  -> eq
                $filters[] = ['field' => $field, 'op' => 'eq', 'values' => self::values($spec)];

                continue;
            }
            if (array_is_list($spec)) { // filter[field][]=a&filter[field][]=b -> eq with several values (in)
                $filters[] = ['field' => $field, 'op' => 'eq', 'values' => self::values($spec)];

                continue;
            }
            foreach ($spec as $op => $value) {
                if (is_array($value) && ! array_is_list($value)) {
                    $bad[] = ['field' => "filter[$field][$op]", 'message' => 'expected filter[field][operator]=value'];

                    continue;
                }
                $filters[] = ['field' => $field, 'op' => (string) $op, 'values' => self::values($value)];
            }
        }

        $sort = [];
        foreach (explode(',', (string) ($q['sort'] ?? '')) as $part) {
            $part = trim($part);
            if ($part === '') {
                continue;
            }
            $sort[] = ['field' => ltrim($part, '+-'), 'desc' => str_starts_with($part, '-')];
        }

        $page = self::int($q, 'page', $bad);
        $size = self::int($q, 'page_size', $bad);
        if ($bad !== []) {
            throw AppException::invalid('invalid list parameters', $bad);
        }
        $cursor = isset($q['cursor']) && is_string($q['cursor']) && $q['cursor'] !== '' ? $q['cursor'] : null;

        return new self($filters, $sort, $page, $size, $cursor);
    }

    /** @return list<string> */
    private static function values(mixed $v): array
    {
        $out = [];
        foreach ((array) $v as $item) {
            if (is_array($item)) {
                continue;
            }
            foreach (explode(',', (string) $item) as $p) {
                $out[] = trim($p);
            }
        }

        return $out;
    }

    /** @param array<string, mixed> $q @param list<array{field: string, message: string}> $bad */
    private static function int(array $q, string $key, array &$bad): int
    {
        if (! isset($q[$key]) || $q[$key] === '') {
            return 0;
        }
        if (! is_scalar($q[$key]) || ! preg_match('/^\d{1,9}$/', (string) $q[$key])) {
            $bad[] = ['field' => $key, 'message' => 'must be a non-negative integer'];

            return 0;
        }

        return (int) $q[$key];
    }
}
