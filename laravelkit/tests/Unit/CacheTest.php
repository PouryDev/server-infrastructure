<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Unit;

use Illuminate\Cache\ArrayStore;
use Illuminate\Cache\Repository;
use PHPUnit\Framework\TestCase;
use PouryDev\LaravelKit\Cache\KitCache;
use PouryDev\LaravelKit\Cache\ReadModel;

/** A store that records the TTL of every write and can be switched off. */
final class SpyRepository extends Repository
{
    /** @var array<string, int> */
    public array $ttls = [];

    public bool $down = false;

    public function __construct()
    {
        parent::__construct(new ArrayStore);
    }

    public function put($key, $value, $ttl = null): bool
    {
        $this->check();
        if (is_string($key)) {
            $this->ttls[$key] = (int) $ttl;
        }

        return parent::put($key, $value, $ttl);
    }

    public function get($key, $default = null): mixed
    {
        $this->check();

        return parent::get($key, $default);
    }

    public function many(array $keys): array
    {
        $this->check();

        return parent::many($keys);
    }

    private function check(): void
    {
        if ($this->down) {
            throw new \RuntimeException('redis down');
        }
    }
}

final class CacheTest extends TestCase
{
    private function cache(?SpyRepository &$store = null, float $jitter = 0.0, float $refresh = 0.0): KitCache
    {
        $store = new SpyRepository;

        return new KitCache($store, 'app', $jitter, 3600, 600, $refresh);
    }

    public function test_remember_loads_once_then_hits(): void
    {
        $c = $this->cache();
        $calls = 0;
        $load = function () use (&$calls) {
            $calls++;

            return ['id' => 1];
        };
        $this->assertSame(['id' => 1], $c->remember('k', 60, $load));
        $this->assertSame(['id' => 1], $c->remember('k', 60, $load));
        $this->assertSame(1, $calls);
    }

    public function test_every_key_must_expire(): void
    {
        $c = $this->cache();
        foreach ([0, -1, 3601] as $bad) {
            try {
                $c->remember('k', $bad, fn () => 1);
                $this->fail("ttl $bad accepted");
            } catch (\InvalidArgumentException) {
            }
        }
        $this->expectException(\InvalidArgumentException::class);
        $c->put('k', 1, 0);
    }

    public function test_jitter_stays_within_bounds_and_actually_varies(): void
    {
        $c = $this->cache($s, 0.1);
        $seen = [];
        for ($i = 0; $i < 200; $i++) {
            $c->put("k$i", 1, 1000);
            $seen[$s->ttls["k$i"]] = true;
        }
        $this->assertGreaterThan(10, count($seen));
        $this->assertGreaterThanOrEqual(900, min(array_keys($seen)));
        $this->assertLessThanOrEqual(1100, max(array_keys($seen)));
    }

    public function test_null_is_cached_only_when_asked_and_briefly(): void
    {
        $c = $this->cache($s);
        $calls = 0;
        $miss = function () use (&$calls) {
            $calls++;

            return null;
        };
        $c->remember('a', 60, $miss);
        $c->remember('a', 60, $miss);
        $this->assertSame(2, $calls, 'null is not cached by default');

        $c->remember('b', 60, $miss, negativeTtl: 20);
        $this->assertNull($c->remember('b', 60, $miss, negativeTtl: 20));
        $this->assertSame(3, $calls);
        $this->assertSame(20, $s->ttls['b']);
    }

    public function test_a_store_outage_fails_open_and_never_throws(): void
    {
        $c = $this->cache($s);
        $s->down = true;
        $this->assertSame('db', $c->remember('k', 60, fn () => 'db'));
        $c->put('x', 1, 60); // swallowed
        $this->assertSame([], $c->many(['x']));
    }

    public function test_loader_errors_propagate_and_are_not_cached(): void
    {
        $c = $this->cache();
        try {
            $c->remember('k', 60, fn () => throw new \DomainException('db error'));
            $this->fail();
        } catch (\DomainException) {
        }
        $this->assertSame('ok', $c->remember('k', 60, fn () => 'ok'));
    }

    public function test_bump_makes_every_old_scoped_key_unreachable(): void
    {
        $c = $this->cache();
        $k1 = $c->scopedKey('product', 7);
        $c->put($k1, 'old', 60);
        $this->assertStringStartsWith('app:product:v', $k1);
        $this->assertSame($k1, $c->scopedKey('product', 7), 'stable without a bump');
        $c->bump('product');
        $k2 = $c->scopedKey('product', 7);
        $this->assertNotSame($k1, $k2);
        $this->assertSame('new', $c->remember($k2, 60, fn () => 'new'));
        $this->assertSame($c->scopedKey('product', 7), $k2);
        $this->assertNotSame($k2, $c->scopedKey('other', 7));
    }

    public function test_bump_is_seen_by_other_processes_within_the_refresh_window(): void
    {
        $store = new SpyRepository;
        $a = new KitCache($store, 'app', 0.0, 3600, 600, 0.0);
        $b = new KitCache($store, 'app', 0.0, 3600, 600, 0.0);
        $before = $b->scopedKey('p', 1);
        $a->bump('p');
        $this->assertNotSame($before, $b->scopedKey('p', 1));
    }

    public function test_read_model_get_many_does_one_batched_load_for_the_misses(): void
    {
        $c = $this->cache();
        $rm = new ReadModel($c, 'card', 60, fn (string $id) => "card-$id");
        $rm->put(1, 'cached-1');
        $batches = [];
        $out = $rm->getMany([1, 2, 3, 2], function (array $missing) use (&$batches) {
            $batches[] = $missing;

            return array_combine($missing, array_map(fn ($i) => "built-$i", $missing));
        });
        $this->assertSame([['2', '3']], $batches);
        $this->assertSame(['1' => 'cached-1', '2' => 'built-2', '3' => 'built-3'], $out);
        $again = $rm->getMany([1, 2, 3], function () use (&$batches) {
            $batches[] = 'unexpected';

            return [];
        });
        $this->assertCount(1, $batches, 'second call is served entirely from the cache');
        $this->assertSame('built-2', $again['2']);
        $rm->invalidate(2);
        $this->assertSame('card-2', $rm->get(2));
    }
}
