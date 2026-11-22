<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Query;

/** One queryable attribute. `column` may be table-qualified ("u.email"). */
final class Field
{
    /** @param list<string> $operators empty = every operator that fits the type */
    public function __construct(
        public readonly string $name,
        public readonly string $column,
        public readonly FieldType $type,
        public readonly bool $filterable = true,
        public readonly bool $sortable = true,
        public readonly array $operators = [],
    ) {}

    public function allows(string $op): bool
    {
        return in_array($op, $this->operators ?: $this->type->operators(), true);
    }
}
