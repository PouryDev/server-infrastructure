<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Cache;

use Closure;

/**
 * A purpose-built view (card, summary, page model) served from Redis and rebuilt from the database on a miss.
 * Eventually consistent: never the source of truth, and never for anything that must be exact.
 *
 *   $card = new ReadModel($cache, 'product-card', 3600, fn (string $id) => ProductCard::build($id));
 *   $card->get($id);                         // Redis first
 *   $card->put($id, $fresh);                 // after a commit: write-through
 *   $card->getMany($ids, fn (array $missing) => ProductCard::buildMany($missing)); // MGET + ONE query
 */
final class ReadModel
{
    /** @param Closure(string): mixed $rebuild returns null when the entity does not exist */
    public function __construct(
        private readonly KitCache $cache,
        private readonly string $name,
        private readonly int $ttl,
        private readonly Closure $rebuild,
        private readonly ?int $negativeTtl = 30,
    ) {}

    public function get(string|int $id): mixed
    {
        return $this->cache->remember($this->key($id), $this->ttl, fn () => ($this->rebuild)((string) $id), $this->negativeTtl);
    }

    public function put(string|int $id, mixed $value): void
    {
        $this->cache->put($this->key($id), $value, $this->ttl);
    }

    public function invalidate(string|int $id): void
    {
        $this->cache->delete($this->key($id));
    }

    /**
     * @param  list<string|int>  $ids
     * @param  Closure(list<string>): array<string, mixed>  $loadMissing  one batched query: id => value
     * @return array<string, mixed> id => value for the ids that exist
     */
    public function getMany(array $ids, Closure $loadMissing): array
    {
        $ids = array_values(array_unique(array_map('strval', $ids)));
        $keys = [];
        foreach ($ids as $id) {
            $keys[$id] = $this->key($id);
        }
        $hits = $this->cache->many(array_values($keys));
        $out = [];
        $missing = [];
        foreach ($keys as $id => $key) {
            if (array_key_exists($key, $hits)) {
                if ($hits[$key] !== '__laravelkit_negative__') {
                    $out[$id] = $hits[$key];
                }
            } else {
                $missing[] = (string) $id;
            }
        }
        if ($missing !== []) {
            foreach ($loadMissing($missing) as $id => $value) {
                $out[(string) $id] = $value;
                $this->put($id, $value);
            }
        }

        return $out;
    }

    private function key(string|int $id): string
    {
        return $this->cache->key('rm', $this->name, (string) $id);
    }
}
