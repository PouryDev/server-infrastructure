<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Query;

use LogicException;

/**
 * What clients may filter and sort by, once per resource. Column names come only from here (never from the
 * request); the request supplies field names that must exist, operators that must fit the type, and values.
 *
 *   $schema = Schema::make()
 *       ->field('id', FieldType::Uuid)->field('email', FieldType::String)->field('created_at', FieldType::Time)
 *       ->field('notes', FieldType::String)->deny('notes')           // black list (allowOnly* = white list)
 *       ->defaultSort('-created_at')->tieBreaker('id')->pageSize(20, 100);
 *
 *   return Lister::paginate(User::query()->where('tenant_id', $t), $schema, Params::fromRequest($request));
 */
final class Schema
{
    /** @var array<string, Field> */
    private array $fields = [];

    /** @var list<array{field: string, desc: bool}> */
    private array $defaultSort = [];

    private ?string $tieBreaker = null;

    private int $defaultPageSize = 20;

    private int $maxPageSize = 100;

    private int $maxOffset = 100_000;

    private int $maxFilters = 20;

    private int $maxListValues = 100;

    private int $maxSortKeys = 3;

    public static function make(): self
    {
        return new self;
    }

    /** @param list<string> $operators narrow the allowed operators */
    public function field(string $name, FieldType $type, ?string $column = null, array $operators = []): self
    {
        $this->fields[$name] = new Field($name, $column ?? $name, $type, true, true, $operators);

        return $this;
    }

    /** Neither filterable nor sortable (it still exists, so a typo elsewhere is caught). */
    public function deny(string ...$names): self
    {
        return $this->denyFilter(...$names)->denySort(...$names);
    }

    public function denyFilter(string ...$names): self
    {
        foreach ($this->known($names) as $n) {
            $f = $this->fields[$n];
            $this->fields[$n] = new Field($f->name, $f->column, $f->type, false, $f->sortable, $f->operators);
        }

        return $this;
    }

    public function denySort(string ...$names): self
    {
        foreach ($this->known($names) as $n) {
            $f = $this->fields[$n];
            $this->fields[$n] = new Field($f->name, $f->column, $f->type, $f->filterable, false, $f->operators);
        }

        return $this;
    }

    /** White list: every other field stops being filterable. */
    public function allowOnlyFilter(string ...$names): self
    {
        return $this->denyFilter(...array_values(array_diff(array_keys($this->fields), $this->known($names))));
    }

    public function allowOnlySort(string ...$names): self
    {
        return $this->denySort(...array_values(array_diff(array_keys($this->fields), $this->known($names))));
    }

    /** @param string ...$sorts "-created_at", "name" */
    public function defaultSort(string ...$sorts): self
    {
        $this->defaultSort = [];
        foreach ($sorts as $s) {
            $name = ltrim($s, '+-');
            $this->known([$name]);
            $this->defaultSort[] = ['field' => $name, 'desc' => str_starts_with($s, '-')];
        }

        return $this;
    }

    /** Unique column appended to every ORDER BY so pages are stable. */
    public function tieBreaker(string $column): self
    {
        $this->tieBreaker = $column;

        return $this;
    }

    public function pageSize(int $default, int $max): self
    {
        if ($default < 1 || $max < $default) {
            throw new LogicException("invalid page sizes (default $default, max $max)");
        }
        [$this->defaultPageSize, $this->maxPageSize] = [$default, $max];

        return $this;
    }

    public function limits(int $maxFilters, int $maxListValues, int $maxSortKeys, int $maxOffset): self
    {
        [$this->maxFilters, $this->maxListValues, $this->maxSortKeys, $this->maxOffset] = [$maxFilters, $maxListValues, $maxSortKeys, $maxOffset];

        return $this;
    }

    /**
     * A stricter copy for one request (for example a narrower allow list for non-admins). Restrictions only
     * accumulate; a name that is not a field is an error, so a mistyped entry cannot silently expose a field.
     *
     * @param  list<string>  $denyFilter  @param list<string> $denySort
     */
    public function restrict(array $denyFilter = [], array $denySort = [], ?int $maxPageSize = null): self
    {
        $c = clone $this;
        $c->denyFilter(...$denyFilter)->denySort(...$denySort);
        if ($maxPageSize !== null) {
            $c->maxPageSize = min($c->maxPageSize, $maxPageSize);
            $c->defaultPageSize = min($c->defaultPageSize, $c->maxPageSize);
        }

        return $c;
    }

    public function getField(string $name): ?Field
    {
        return $this->fields[$name] ?? null;
    }

    /** @internal */
    public function limitsArray(): array
    {
        return ['default_page_size' => $this->defaultPageSize, 'max_page_size' => $this->maxPageSize,
            'max_offset' => $this->maxOffset, 'max_filters' => $this->maxFilters,
            'max_list_values' => $this->maxListValues, 'max_sort_keys' => $this->maxSortKeys];
    }

    /** @return list<array{field: string, desc: bool}> @internal */
    public function getDefaultSort(): array
    {
        return $this->defaultSort;
    }

    /** @internal */
    public function getTieBreaker(): ?string
    {
        return $this->tieBreaker;
    }

    /** @param list<string> $names @return list<string> */
    private function known(array $names): array
    {
        foreach ($names as $n) {
            if (! isset($this->fields[$n])) {
                throw new LogicException("query schema: unknown field '$n'");
            }
        }

        return $names;
    }
}
