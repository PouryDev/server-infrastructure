<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Cache;

use Illuminate\Redis\Connections\Connection;

/**
 * Counters and leaderboards straight on the shared Redis connection (the connection's `prefix` option, which
 * must equal REDIS_KEY_PREFIX, is applied by Laravel). Every key gets a TTL on every write.
 */
final class Structures
{
    public function __construct(private readonly Connection $redis, private readonly string $namespace = 'kit', private readonly int $maxTtl = 86400) {}

    /** Increments one field of a hash (a counter family) and refreshes the hash TTL. */
    public function hincr(string $name, string $field, int $by = 1, int $ttl = 3600): int
    {
        $key = $this->namespace.':hash:'.$name;
        $this->ttl($ttl);
        $n = (int) $this->redis->hincrby($key, $field, $by);
        $this->redis->expire($key, $ttl);

        return $n;
    }

    /** @return array<string, int> */
    public function hgetall(string $name): array
    {
        return array_map('intval', (array) $this->redis->hgetall($this->namespace.':hash:'.$name));
    }

    /** Adds `$by` to a member's score, keeps at most `$cap` members (the lowest are dropped) and refreshes the TTL. */
    public function rankIncr(string $name, string $member, float $by = 1.0, int $ttl = 3600, int $cap = 1000): float
    {
        $key = $this->namespace.':rank:'.$name;
        $this->ttl($ttl);
        $score = (float) $this->redis->zincrby($key, $by, $member);
        $this->redis->zremrangebyrank($key, 0, -($cap + 1));
        $this->redis->expire($key, $ttl);

        return $score;
    }

    /** @return array<string, float> member => score, best first */
    public function rankTop(string $name, int $n = 10): array
    {
        $rows = (array) $this->redis->zrevrange($this->namespace.':rank:'.$name, 0, max(0, $n - 1), ['withscores' => true]);

        return array_map('floatval', $rows);
    }

    /** 1-based rank (1 = best) or null. */
    public function rank(string $name, string $member): ?int
    {
        $r = $this->redis->zrevrank($this->namespace.':rank:'.$name, $member);

        return $r === null || $r === false ? null : ((int) $r) + 1;
    }

    private function ttl(int $ttl): void
    {
        if ($ttl <= 0 || $ttl > $this->maxTtl) {
            throw new \InvalidArgumentException("TTL must be 1..{$this->maxTtl} seconds (every key must expire)");
        }
    }
}
