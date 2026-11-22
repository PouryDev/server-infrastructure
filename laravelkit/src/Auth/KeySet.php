<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

use InvalidArgumentException;

/**
 * Keys by id. The first key signs; every key verifies, so rotation = put the new key first and drop the old
 * one after its tokens expired.
 */
final class KeySet
{
    /** @var array<string, Key> */
    private array $byId = [];

    public function __construct(Key ...$keys)
    {
        if ($keys === []) {
            throw new InvalidArgumentException('at least one JWT key is required');
        }
        foreach ($keys as $k) {
            if (isset($this->byId[$k->id])) {
                throw new InvalidArgumentException("duplicate JWT key id '{$k->id}'");
            }
            $this->byId[$k->id] = $k;
        }
    }

    public function signer(): Key
    {
        return array_values($this->byId)[0];
    }

    /** @return list<string> */
    public function ids(): array
    {
        return array_keys($this->byId);
    }

    public function get(string $id): ?Key
    {
        return $this->byId[$id] ?? null;
    }

    /** @param array<string, mixed> $cfg the "auth" part of config/laravelkit.php */
    public static function fromConfig(array $cfg): self
    {
        $id = (string) ($cfg['key_id'] ?? 'k1');
        $first = match (true) {
            ! empty($cfg['private_key']) => Key::ed25519($id, (string) $cfg['private_key']),
            ! empty($cfg['public_key']) => Key::ed25519Public($id, (string) $cfg['public_key']),
            ! empty($cfg['secret']) => Key::hmac($id, (string) $cfg['secret']),
            default => throw new InvalidArgumentException('set JWT_PRIVATE_KEY (issue + verify), JWT_PUBLIC_KEY (verify only) or JWT_SECRET'),
        };
        $keys = [$first];
        foreach ($cfg['previous_keys'] ?? [] as $p) {
            $keys[] = match (true) {
                ! empty($p['public_key']) => Key::ed25519Public((string) $p['id'], (string) $p['public_key']),
                ! empty($p['private_key']) => Key::ed25519((string) $p['id'], (string) $p['private_key']),
                ! empty($p['secret']) => Key::hmac((string) $p['id'], (string) $p['secret']),
                default => throw new InvalidArgumentException("previous JWT key '{$p['id']}' has no material"),
            };
        }

        return new self(...$keys);
    }
}
