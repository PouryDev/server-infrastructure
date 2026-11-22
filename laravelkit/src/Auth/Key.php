<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

use InvalidArgumentException;

/**
 * One signing / verification key. HS256 (shared secret) or EdDSA (Ed25519). A key built from a public key
 * only can verify, never sign: services that merely check tokens never hold the private key.
 *
 * Material is accepted as PKCS#8 / PKIX PEM (what gokit's `jwt-key` prints, so Laravel and Go services can
 * verify each other's tokens) or as base64 of the raw key.
 */
final class Key
{
    private function __construct(
        public readonly string $id,
        public readonly string $alg,           // HS256 | EdDSA
        public readonly string $verifyMaterial, // secret, or base64 raw public key
        public readonly ?string $signMaterial,  // secret, or base64 libsodium secret key (null = verify-only)
    ) {}

    public function canSign(): bool
    {
        return $this->signMaterial !== null;
    }

    public static function hmac(string $id, string $secret): self
    {
        $raw = self::decodeSecret($secret);
        if (strlen($raw) < 32) {
            throw new InvalidArgumentException('JWT HS256 secret must be at least 32 bytes');
        }

        return new self($id, 'HS256', $raw, $raw);
    }

    /** Signs and verifies. `$private` is a PKCS#8 PEM or base64 of the 32-byte seed / 64-byte libsodium key. */
    public static function ed25519(string $id, string $private): self
    {
        self::needSodium();
        $raw = str_contains($private, '-----BEGIN') ? self::pemBody($private) : (base64_decode($private, true) ?: '');
        $seed = match (true) {
            strlen($raw) === 48 && str_starts_with($raw, hex2bin('302e020100300506032b657004220420')) => substr($raw, 16),
            strlen($raw) === 32 => $raw,
            strlen($raw) === 64 => substr($raw, 0, 32),
            default => throw new InvalidArgumentException('JWT private key is not a valid Ed25519 key'),
        };
        $pair = sodium_crypto_sign_seed_keypair($seed);

        return new self($id, 'EdDSA', base64_encode(sodium_crypto_sign_publickey($pair)), base64_encode(sodium_crypto_sign_secretkey($pair)));
    }

    /** Verify-only. `$public` is a PKIX PEM or base64 of the 32-byte key. */
    public static function ed25519Public(string $id, string $public): self
    {
        self::needSodium();
        $raw = str_contains($public, '-----BEGIN') ? self::pemBody($public) : (base64_decode($public, true) ?: '');
        $pub = match (true) {
            strlen($raw) === 44 && str_starts_with($raw, hex2bin('302a300506032b6570032100')) => substr($raw, 12),
            strlen($raw) === 32 => $raw,
            default => throw new InvalidArgumentException('JWT public key is not a valid Ed25519 key'),
        };

        return new self($id, 'EdDSA', base64_encode($pub), null);
    }

    /** @return array{private: string, public: string, private_pem: string, public_pem: string} */
    public static function generateEd25519(): array
    {
        self::needSodium();
        $pair = sodium_crypto_sign_keypair();
        $seed = substr(sodium_crypto_sign_secretkey($pair), 0, 32);
        $pub = sodium_crypto_sign_publickey($pair);
        $pem = static fn (string $label, string $der) => "-----BEGIN $label-----\n".chunk_split(base64_encode($der), 64, "\n")."-----END $label-----\n";

        return [
            'private' => base64_encode($seed), 'public' => base64_encode($pub),
            'private_pem' => $pem('PRIVATE KEY', hex2bin('302e020100300506032b657004220420').$seed),
            'public_pem' => $pem('PUBLIC KEY', hex2bin('302a300506032b6570032100').$pub),
        ];
    }

    private static function decodeSecret(string $s): string
    {
        $d = base64_decode($s, true);

        return $d !== false && strlen($d) >= 32 ? $d : $s;
    }

    /** Also accepts PEM kept in a single-line env var with literal "\n". */
    private static function pemBody(string $pem): string
    {
        $pem = str_replace('\n', "\n", $pem);
        $body = preg_replace('/-----(BEGIN|END)[^-]+-----|\s+/', '', $pem) ?? '';

        return base64_decode($body, true) ?: '';
    }

    private static function needSodium(): void
    {
        if (! function_exists('sodium_crypto_sign_keypair')) {
            throw new InvalidArgumentException('EdDSA keys need ext-sodium');
        }
    }
}
