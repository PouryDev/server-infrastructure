<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Query;

use Illuminate\Contracts\Support\Arrayable;
use Illuminate\Contracts\Support\Responsable;
use Illuminate\Http\JsonResponse;
use JsonSerializable;

/** Response shape shared with the Go services: {"items": [...], "meta": {...}}. */
final class Page implements Arrayable, JsonSerializable, Responsable
{
    /** @param list<mixed> $items @param array<string, mixed> $meta */
    public function __construct(public readonly array $items, public readonly array $meta) {}

    public function toArray(): array
    {
        return ['items' => array_map(
            static fn ($i) => $i instanceof Arrayable ? $i->toArray() : $i,
            $this->items,
        ), 'meta' => $this->meta];
    }

    public function jsonSerialize(): array
    {
        return $this->toArray();
    }

    public function toResponse($request): JsonResponse
    {
        return new JsonResponse($this->toArray(), 200, ['Cache-Control' => 'no-store'], JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES);
    }
}
