<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Cache;

use Closure;
use Illuminate\Contracts\Cache\LockProvider;
use Illuminate\Contracts\Cache\LockTimeoutException;
use Illuminate\Contracts\Cache\Repository;
use InvalidArgumentException;
use Psr\Log\LoggerInterface;
use Psr\Log\NullLogger;

/**
 * The shared-Redis cache contract on top of Laravel's cache repository:
 *
 *  - every key expires (a TTL is mandatory and capped), with random jitter so keys written together do not
 *    expire together;
 *  - fail open: a store error is logged and the value is loaded from the source, a cache outage never fails
 *    a request;
 *  - stampede protection: concurrent misses run one loader (an atomic lock), the others wait briefly for it;
 *  - "not found" is cached only on request, briefly;
 *  - invalidation without KEYS: `scopedKey()` puts a version in the key, `bump()` makes every old key unreachable.
 *
 * Caching is opt-in and belongs in a decorator over a repository, never in the models. Whatever is cached must
 * be rebuildable from the database; money, stock and authorisation decisions read the database.
 */
final class KitCache
{
    private const NEGATIVE = '__laravelkit_negative__';

    /** @var array<string, array{string, float}> */
    private array $versions = [];

    public function __construct(
        private readonly Repository $store,
        private readonly string $namespace = 'kit',
        private readonly float $jitter = 0.1,
        private readonly int $maxTtl = 86400,
        private readonly int $versionTtl = 86400,
        private readonly float $versionRefresh = 1.0,
        private readonly LoggerInterface $log = new NullLogger,
    ) {
        if ($jitter < 0 || $jitter >= 1) {
            throw new InvalidArgumentException('jitter must be in [0, 1)');
        }
    }

    public function key(string ...$parts): string
    {
        return $this->namespace.':'.implode(':', $parts);
    }

    /** Key whose version changes on `bump($scope)`: "everything about <scope>" dies together. */
    public function scopedKey(string $scope, string|int $id): string
    {
        return $this->key($scope, 'v'.$this->version($scope), (string) $id);
    }

    /** Every key built with `scopedKey($scope, ...)` before this call is never read again and expires by itself. */
    public function bump(string $scope): void
    {
        $v = (string) (int) (microtime(true) * 1_000_000);
        try {
            $this->store->put($this->versionKey($scope), $v, $this->versionTtl);
        } catch (\Throwable $e) {
            $this->log->warning('cache bump failed', ['scope' => $scope, 'error' => $e->getMessage()]);

            throw $e; // an invalidation that did not happen must not pass silently
        }
        $this->versions[$scope] = [$v, microtime(true)];
    }

    public function delete(string $key): void
    {
        try {
            $this->store->forget($key);
        } catch (\Throwable $e) {
            $this->log->warning('cache delete failed', ['key' => $key, 'error' => $e->getMessage()]);

            throw $e;
        }
    }

    /**
     * @template T
     *
     * @param  Closure(): T  $loader
     * @param  int|null  $negativeTtl  seconds to remember a `null` result ("no such row"); null = never cache null
     * @return T
     */
    public function remember(string $key, int $ttl, Closure $loader, ?int $negativeTtl = null): mixed
    {
        $this->checkTtl($ttl);
        try {
            $hit = $this->read($key);
            if ($hit !== null) {
                return $hit === self::NEGATIVE ? null : $hit;
            }
        } catch (\Throwable $e) {
            return $this->failOpen($key, $e, $loader);
        }

        try {
            $lock = $this->store->getStore() instanceof LockProvider
                ? $this->store->getStore()->lock($this->namespace.':lock:'.$key, 10) : null;
        } catch (\Throwable) {
            $lock = null;
        }
        if ($lock === null) {
            return $this->load($key, $ttl, $loader, $negativeTtl);
        }

        try {
            // the lock holder loads; everyone else waits up to 3 s, re-reads, and loads itself only if still missing
            return $lock->block(3, function () use ($key, $ttl, $loader, $negativeTtl) {
                $hit = $this->read($key);
                if ($hit !== null) {
                    return $hit === self::NEGATIVE ? null : $hit;
                }

                return $this->load($key, $ttl, $loader, $negativeTtl);
            });
        } catch (LockTimeoutException) {
            return $this->load($key, $ttl, $loader, $negativeTtl);
        } catch (\Throwable $e) {
            return $this->failOpen($key, $e, $loader);
        }
    }

    /** Write-through: store a value you just produced. Failures are logged, never thrown. */
    public function put(string $key, mixed $value, int $ttl): void
    {
        $this->checkTtl($ttl);
        try {
            $this->store->put($key, $value, $this->withJitter($ttl));
        } catch (\Throwable $e) {
            $this->log->warning('cache write failed', ['key' => $key, 'error' => $e->getMessage()]);
        }
    }

    /**
     * One round trip for many keys. A store error returns an empty array (everything is a miss).
     *
     * @param  list<string>  $keys  @return array<string, mixed> only the hits, by key
     */
    public function many(array $keys): array
    {
        if ($keys === []) {
            return [];
        }
        try {
            $out = [];
            foreach ($this->store->many($keys) as $k => $v) {
                if ($v !== null) {
                    $out[$k] = $v;
                }
            }

            return $out;
        } catch (\Throwable $e) {
            $this->log->warning('cache read failed', ['keys' => count($keys), 'error' => $e->getMessage()]);

            return [];
        }
    }

    public function store(): Repository
    {
        return $this->store;
    }

    private function read(string $key): mixed
    {
        return $this->store->get($key);
    }

    private function load(string $key, int $ttl, Closure $loader, ?int $negativeTtl): mixed
    {
        $value = $loader(); // loader errors are never cached and always propagate
        try {
            if ($value !== null) {
                $this->store->put($key, $value, $this->withJitter($ttl));
            } elseif ($negativeTtl !== null) {
                $this->checkTtl($negativeTtl);
                $this->store->put($key, self::NEGATIVE, $this->withJitter($negativeTtl));
            }
        } catch (\Throwable $e) {
            $this->log->warning('cache write failed', ['key' => $key, 'error' => $e->getMessage()]);
        }

        return $value;
    }

    private function failOpen(string $key, \Throwable $e, Closure $loader): mixed
    {
        $this->log->warning('cache unavailable, loading from the source', ['key' => $key, 'error' => $e->getMessage()]);

        return $loader();
    }

    private function checkTtl(int $ttl): void
    {
        if ($ttl <= 0 || $ttl > $this->maxTtl) {
            throw new InvalidArgumentException("cache TTL must be 1..{$this->maxTtl} seconds, got $ttl (every key must expire)");
        }
    }

    private function withJitter(int $ttl): int
    {
        $delta = (int) round($ttl * $this->jitter);
        if ($delta === 0) {
            return $ttl;
        }

        return max(1, min($this->maxTtl, $ttl + random_int(-$delta, $delta)));
    }

    private function versionKey(string $scope): string
    {
        return $this->key('ver', $scope);
    }

    /** The version is re-read at most every `versionRefresh` seconds per process, which bounds staleness. */
    private function version(string $scope): string
    {
        $now = microtime(true);
        if (isset($this->versions[$scope]) && $now - $this->versions[$scope][1] < $this->versionRefresh) {
            return $this->versions[$scope][0];
        }
        try {
            $v = $this->store->get($this->versionKey($scope));
            if ($v === null) {
                $v = (string) (int) ($now * 1_000_000);
                $this->store->add($this->versionKey($scope), $v, $this->versionTtl);
                $v = (string) $this->store->get($this->versionKey($scope), $v);
            }
        } catch (\Throwable $e) {
            $this->log->warning('cache version unavailable', ['scope' => $scope, 'error' => $e->getMessage()]);
            $v = $this->versions[$scope][0] ?? '0';
        }

        return ($this->versions[$scope] = [(string) $v, $now])[0];
    }
}
