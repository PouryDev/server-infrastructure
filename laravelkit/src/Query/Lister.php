<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Query;

use Carbon\CarbonImmutable;
use Illuminate\Database\Eloquent\Builder as EloquentBuilder;
use Illuminate\Database\Query\Builder as QueryBuilder;
use Illuminate\Pagination\Cursor;
use PouryDev\LaravelKit\Errors\AppException;

/**
 * Applies validated client filters / sorting / pagination to an Eloquent or query builder.
 *
 * Safety: column names come only from the {@see Schema}; every value is a bind; LIKE wildcards in user input
 * are escaped; an unknown and a forbidden field produce the same message; filters, list size, sort keys, page
 * size and offset depth are bounded.
 */
final class Lister
{
    private const UUID = '/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i';

    /**
     * Filters and sorting only (no pagination), for exports and "all matching ids".
     *
     * @template T of EloquentBuilder|QueryBuilder
     *
     * @param  T  $query
     * @return T
     */
    public static function apply(EloquentBuilder|QueryBuilder $query, Schema $schema, Params $params, bool $sort = true): EloquentBuilder|QueryBuilder
    {
        $lim = $schema->limitsArray();
        if (count($params->filters) > $lim['max_filters']) {
            throw AppException::invalid('too many filters', ['filter' => "at most {$lim['max_filters']} filters"]);
        }
        $errors = [];
        foreach ($params->filters as $f) {
            try {
                self::filter($query, $schema, $f, $lim['max_list_values']);
            } catch (AppException $e) {
                array_push($errors, ...$e->fields);
            }
        }
        if ($errors !== []) {
            throw AppException::invalid('invalid filters', $errors);
        }
        if ($sort) {
            self::sort($query, $schema, $params, $lim['max_sort_keys']);
        }

        return $query;
    }

    /**
     * Offset pagination: fetches page_size + 1 rows to know has_next and skips count(*) when the first page
     * already holds everything. `$withTotal = false` skips counting always (cheapest on huge tables).
     *
     * @param  (callable(mixed): mixed)|null  $map  transforms each model (for example into an API resource)
     */
    public static function paginate(EloquentBuilder|QueryBuilder $query, Schema $schema, Params $params, bool $withTotal = true, ?callable $map = null): Page
    {
        $lim = $schema->limitsArray();
        $size = $params->pageSize > 0 ? min($params->pageSize, $lim['max_page_size']) : $lim['default_page_size'];
        $page = max(1, $params->page);
        $offset = ($page - 1) * $size;
        if ($offset > $lim['max_offset']) {
            throw AppException::invalid('invalid list parameters', ['page' => 'page is too deep; narrow the filter or use cursor pagination']);
        }

        self::apply($query, $schema, $params);
        $rows = (clone $query)->offset($offset)->limit($size + 1)->get();
        $hasNext = $rows->count() > $size;
        $items = $rows->take($size)->all();

        $total = null;
        if ($withTotal) {
            $total = ($page === 1 && ! $hasNext) ? count($items) : self::count($query);
        }
        $meta = ['page' => $page, 'page_size' => $size, 'has_next' => $hasNext];
        if ($total !== null) {
            $meta['total'] = $total;
            $meta['total_pages'] = (int) ceil($total / $size);
        }

        return new Page(array_values($map ? array_map($map, $items) : $items), $meta);
    }

    /**
     * Cursor (keyset) pagination: constant cost at any depth, no page jumps. Sorting must be on columns that
     * together are unique (the schema's tie-breaker takes care of it).
     */
    public static function cursor(EloquentBuilder|QueryBuilder $query, Schema $schema, Params $params, ?callable $map = null): Page
    {
        $lim = $schema->limitsArray();
        $size = $params->pageSize > 0 ? min($params->pageSize, $lim['max_page_size']) : $lim['default_page_size'];
        self::apply($query, $schema, $params);
        try {
            $cursor = $params->cursor !== null ? Cursor::fromEncoded($params->cursor) : null;
        } catch (\Throwable) {
            $cursor = null;
        }
        if ($params->cursor !== null && $cursor === null) {
            throw AppException::invalid('invalid list parameters', ['cursor' => 'invalid cursor']);
        }
        $p = $query->cursorPaginate($size, ['*'], 'cursor', $cursor);
        $items = $p->items();

        return new Page(array_values($map ? array_map($map, $items) : $items), [
            'page_size' => $size, 'has_next' => $p->hasMorePages(), 'next_cursor' => $p->nextCursor()?->encode(),
        ]);
    }

    private static function count(EloquentBuilder|QueryBuilder $query): int
    {
        $base = $query instanceof EloquentBuilder ? $query->toBase() : clone $query;

        return $base->getCountForPagination();
    }

    /** @param array{field: string, op: string, values: list<string>} $f */
    private static function filter(EloquentBuilder|QueryBuilder $q, Schema $schema, array $f, int $maxValues): void
    {
        $field = $schema->getField($f['field']);
        if ($field === null || ! $field->filterable) {
            throw AppException::invalid('invalid filters', [['field' => "filter[{$f['field']}]", 'message' => 'unknown filter field']]);
        }
        $key = "filter[{$f['field']}]".($f['op'] !== 'eq' ? "[{$f['op']}]" : '');
        $fail = static fn (string $m) => AppException::invalid('invalid filters', [['field' => $key, 'message' => $m]]);
        $op = $f['op'];
        if (! $field->allows($op)) {
            throw $fail("operator '$op' is not supported for this field");
        }
        $values = $f['values'];
        if (count($values) > $maxValues) {
            throw $fail("at most $maxValues values");
        }
        $conv = fn (string $v) => self::convert($field->type, $v, $fail);
        $col = $field->column;
        $one = static function () use ($values, $fail) {
            if (count($values) !== 1 || $values[0] === '') {
                throw $fail('exactly one value expected');
            }

            return $values[0];
        };

        switch ($op) {
            case 'eq':
                if (count($values) > 1) { // filter[a][]=x&filter[a][]=y behaves like "in"
                    $q->whereIn($col, array_map($conv, $values));
                } else {
                    $q->where($col, '=', $conv($one()));
                }
                break;
            case 'ne':
                $q->where($col, '<>', $conv($one()));
                break;
            case 'gt': case 'gte': case 'lt': case 'lte':
                $q->where($col, ['gt' => '>', 'gte' => '>=', 'lt' => '<', 'lte' => '<='][$op], $conv($one()));
                break;
            case 'in': case 'nin':
                $list = array_values(array_filter($values, static fn ($v) => $v !== ''));
                if ($list === []) {
                    throw $fail('at least one value expected');
                }
                $op === 'in' ? $q->whereIn($col, array_map($conv, $list)) : $q->whereNotIn($col, array_map($conv, $list));
                break;
            case 'between':
                $parts = count($values) === 2 ? $values : [];
                if ($parts === [] || $parts[0] === '' || $parts[1] === '') {
                    throw $fail('between needs two values: from,to');
                }
                $q->whereBetween($col, [$conv($parts[0]), $conv($parts[1])]);
                break;
            case 'null':
                $v = strtolower($one());
                if (! in_array($v, ['true', 'false', '1', '0'], true)) {
                    throw $fail('expected true or false');
                }
                in_array($v, ['true', '1'], true) ? $q->whereNull($col) : $q->whereNotNull($col);
                break;
            case 'like': case 'prefix':
                $esc = str_replace(['\\', '%', '_'], ['\\\\', '\\%', '\\_'], $one());
                $pattern = $op === 'like' ? "%$esc%" : "$esc%";
                $grammar = ($q instanceof EloquentBuilder ? $q->getQuery() : $q)->getGrammar();
                $kw = ($q instanceof EloquentBuilder ? $q->getQuery() : $q)->getConnection()->getDriverName() === 'pgsql' ? 'ILIKE' : 'LIKE';
                $q->whereRaw($grammar->wrap($col)." $kw ? ESCAPE '\\'", [$pattern]);
                break;
            default:
                throw $fail("unknown operator '$op'");
        }
    }

    private static function sort(EloquentBuilder|QueryBuilder $q, Schema $schema, Params $params, int $maxKeys): void
    {
        $sorts = $params->sort ?: $schema->getDefaultSort();
        if (count($sorts) > $maxKeys) {
            throw AppException::invalid('invalid sort', ['sort' => "at most $maxKeys sort keys"]);
        }
        $used = [];
        foreach ($sorts as $s) {
            $field = $schema->getField($s['field']);
            if ($field === null || ! $field->sortable) {
                throw AppException::invalid('invalid sort', ['sort' => "unknown sort field '{$s['field']}'"]);
            }
            $q->orderBy($field->column, $s['desc'] ? 'desc' : 'asc');
            $used[] = $field->column;
        }
        $tb = $schema->getTieBreaker();
        if ($tb !== null && ! in_array($tb, $used, true)) {
            $q->orderBy($tb, 'asc');
        }
    }

    private static function convert(FieldType $t, string $raw, callable $fail): mixed
    {
        switch ($t) {
            case FieldType::String:
                return $raw;
            case FieldType::Int:
                if (! preg_match('/^-?\d{1,18}$/', $raw)) {
                    throw $fail("'$raw' is not an integer");
                }

                return (int) $raw;
            case FieldType::Float:
                if (! is_numeric($raw)) {
                    throw $fail("'$raw' is not a number");
                }

                return (float) $raw;
            case FieldType::Bool:
                return match (strtolower($raw)) {
                    'true', '1' => true, 'false', '0' => false,
                    default => throw $fail("'$raw' is not a boolean (true/false)"),
                };
            case FieldType::Uuid:
                if (! preg_match(self::UUID, $raw)) {
                    throw $fail("'$raw' is not a UUID");
                }

                return strtolower($raw);
            case FieldType::Time:
                if (! preg_match('/^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?$/', $raw)) {
                    throw $fail("'$raw' is not a date (RFC 3339 or YYYY-MM-DD)");
                }
                try {
                    return CarbonImmutable::parse($raw)->utc()->format('Y-m-d H:i:s.u');
                } catch (\Throwable) {
                    throw $fail("'$raw' is not a valid date");
                }
        }
    }
}
