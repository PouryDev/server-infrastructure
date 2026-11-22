<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Query;

enum FieldType: string
{
    case String = 'string';
    case Int = 'int';
    case Float = 'float';
    case Bool = 'bool';
    case Time = 'time';
    case Uuid = 'uuid';

    /** @return list<string> operators that make sense for the type */
    public function operators(): array
    {
        return match ($this) {
            self::String => ['eq', 'ne', 'like', 'prefix', 'in', 'nin', 'null'],
            self::Int => ['eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'in', 'nin', 'between', 'null'],
            self::Float, self::Time => ['eq', 'ne', 'gt', 'gte', 'lt', 'lte', 'between', 'null'],
            self::Bool => ['eq', 'ne', 'null'],
            self::Uuid => ['eq', 'ne', 'in', 'nin', 'null'],
        };
    }
}
