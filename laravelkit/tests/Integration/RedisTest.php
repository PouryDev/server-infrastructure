<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Integration;

use Illuminate\Support\Facades\Cache;
use Illuminate\Support\Facades\Redis;
use PouryDev\LaravelKit\Auth\Jwt;
use PouryDev\LaravelKit\Auth\KeySet;
use PouryDev\LaravelKit\Auth\Revocations;
use PouryDev\LaravelKit\Cache\KitCache;
use PouryDev\LaravelKit\Cache\ReadModel;
use PouryDev\LaravelKit\Cache\Structures;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Tests\TestCase;
use Predis\Client;

/** Needs a real Redis: run through `make laravelkit-integration` (LK_REDIS_HOST is set there). */
final class RedisTest extends TestCase
{
    protected function defineEnvironment($app): void
    {
        parent::defineEnvironment($app);
        $host = getenv('LK_REDIS_HOST') ?: '';
        if ($host === '') {
            $this->markTestSkipped('LK_REDIS_HOST not set');
        }
        $app['config']->set('database.redis.client', 'predis');
        $app['config']->set('database.redis.options.prefix', 'shop:');
        $app['config']->set('database.redis.default', ['host' => $host, 'port' => 6379, 'database' => 0]);
        $app['config']->set('database.redis.cache', ['host' => $host, 'port' => 6379, 'database' => 0]);
        // an unreachable Redis, configured up front (RedisManager copies its config when it is created)
        $app['config']->set('database.redis.dead', ['host' => '127.0.0.1', 'port' => 1, 'database' => 0, 'timeout' => 0.2, 'read_timeout' => 0.2]);
        $app['config']->set('cache.stores.redis_dead', ['driver' => 'redis', 'connection' => 'dead', 'lock_connection' => 'dead']);
        $app['config']->set('cache.default', 'redis');
        $app['config']->set('cache.prefix', 'shop');
        $app['config']->set('laravelkit.auth.revocation_store', 'redis');
    }

    protected function setUp(): void
    {
        parent::setUp();
        Redis::connection()->flushdb();
    }

    /** @return list<string> every key as stored, without the client-side prefix */
    private function rawKeys(): array
    {
        $raw = new Client(['host' => getenv('LK_REDIS_HOST'), 'port' => 6379]);

        return $raw->keys('*');
    }

    public function test_every_key_the_kit_writes_has_the_prefix_and_a_ttl(): void
    {
        $c = $this->app->make(KitCache::class);
        $c->remember('a', 60, fn () => 1);
        $c->put($c->scopedKey('p', 1), 'x', 60);
        $c->bump('p');
        (new ReadModel($c, 'card', 60, fn () => 'v'))->get(1);
        $s = new Structures(Redis::connection());
        $s->hincr('hits', 'home');
        $s->rankIncr('top', 'a');
        $j = $this->app->make(Jwt::class);
        $this->app->make(Revocations::class)->revoke($j->verify($j->issuePair('u')->accessToken));

        $raw = new Client(['host' => getenv('LK_REDIS_HOST'), 'port' => 6379]);
        $keys = $this->rawKeys();
        $this->assertGreaterThan(5, count($keys));
        foreach ($keys as $k) {
            $this->assertStringStartsWith('shop:', $k, 'contract: every key starts with REDIS_KEY_PREFIX');
            $this->assertGreaterThan(0, $raw->ttl($k), "$k must have a TTL");
        }
    }

    public function test_remember_across_processes_runs_one_loader_under_contention(): void
    {
        $c = $this->app->make(KitCache::class);
        $calls = 0;
        $v1 = $c->remember('hot', 60, function () use (&$calls) {
            $calls++;
            usleep(50_000);

            return 'value';
        });
        $v2 = $c->remember('hot', 60, function () use (&$calls) {
            $calls++;

            return 'other';
        });
        $this->assertSame(['value', 'value', 1], [$v1, $v2, $calls]);
        $this->assertSame([], array_filter($this->rawKeys(), fn ($k) => str_contains($k, ':lock:') && Redis::connection()->ttl(substr($k, 5)) === -1), 'locks do not leak');
    }

    public function test_bump_invalidates_through_real_redis_and_old_keys_expire_on_their_own(): void
    {
        $c = $this->app->make(KitCache::class);
        $k1 = $c->scopedKey('user', 5);
        $c->put($k1, 'old', 60);
        $c->bump('user');
        $this->assertNotSame($k1, $c->scopedKey('user', 5));
        $this->assertSame('fresh', $c->remember($c->scopedKey('user', 5), 60, fn () => 'fresh'));
    }

    public function test_many_is_one_mget(): void
    {
        $c = $this->app->make(KitCache::class);
        $c->put('m1', 'a', 60);
        $c->put('m2', 'b', 60);
        $this->assertSame(['m1' => 'a', 'm2' => 'b'], $c->many(['m1', 'm2', 'm3']));
    }

    public function test_structures(): void
    {
        $s = new Structures(Redis::connection());
        $this->assertSame(1, $s->hincr('hits', 'home'));
        $this->assertSame(3, $s->hincr('hits', 'home', 2));
        $this->assertSame(['home' => 3], $s->hgetall('hits'));
        foreach (['a' => 5, 'b' => 9, 'c' => 1, 'd' => 7] as $m => $score) {
            $s->rankIncr('top', $m, $score, 60, cap: 3);
        }
        $this->assertSame(['b' => 9.0, 'd' => 7.0, 'a' => 5.0], $s->rankTop('top', 10), 'capped to 3 members, lowest dropped');
        $this->assertSame(1, $s->rank('top', 'b'));
        $this->assertNull($s->rank('top', 'c'));
        $this->expectException(\InvalidArgumentException::class);
        $s->hincr('x', 'y', 1, 0);
    }

    public function test_token_revocation_over_real_redis(): void
    {
        $j = $this->app->make(Jwt::class);
        $rev = $this->app->make(Revocations::class);
        $a = $j->issuePair('u1')->accessToken;
        $rev->revoke($j->verify($a));
        try {
            $j->verify($a);
            $this->fail();
        } catch (AppException $e) {
            $this->assertSame('token_revoked', $e->errorCode);
        }
        $this->assertSame('u2', $j->verify($j->issuePair('u2')->accessToken)->subject);
        $b = $j->issuePair('u3')->accessToken;
        $rev->revokeSubject('u3');
        $this->expectException(AppException::class);
        $j->verify($b);
    }

    public function test_when_redis_goes_away_the_cache_fails_open_but_authentication_fails_closed(): void
    {
        $j = $this->app->make(Jwt::class);
        $t = $j->issuePair('u')->accessToken;
        $dead = Cache::store('redis_dead');

        $cache = new KitCache($dead, 'kit', 0.0, 3600, 600, 0.0);
        $this->assertSame('db', $cache->remember('k', 60, fn () => 'db'), 'cache outage = read from the database');
        $this->assertSame([], $cache->many(['a']));
        $cache->put('a', 1, 60); // swallowed

        $strict = new Jwt($this->app->make(KeySet::class), 'test-issuer', 'test-aud', 900, 3600, 30, new Revocations($dead));
        try {
            $strict->verify($t);
            $this->fail('must fail closed');
        } catch (AppException $e) {
            $this->assertSame(503, $e->kind->status());
            $this->assertSame('revocation_check_failed', $e->errorCode);
        }
    }
}
