<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

use Firebase\JWT\ExpiredException;
use Firebase\JWT\JWT as FirebaseJwt;
use Firebase\JWT\Key as FirebaseKey;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Errors\Kind;
use stdClass;
use Throwable;

/**
 * Stateless tokens: validity = signature + issuer + audience + type + expiry (+ an optional revocation check).
 * Access tokens are short (default 15 min) and carry roles; refresh tokens (30 d) carry identity only, so the
 * role is re-read when they are exchanged. The algorithm is pinned per key id (no algorithm confusion).
 */
final class Jwt
{
    /** @param int $leeway seconds of clock skew tolerated when verifying */
    public function __construct(
        private readonly KeySet $keys,
        private readonly string $issuer,
        private readonly string $audience,
        private readonly int $accessTtl = 900,
        private readonly int $refreshTtl = 2_592_000,
        private readonly int $leeway = 30,
        private readonly ?Revocations $revocations = null,
        private readonly ?\Closure $clock = null,
    ) {}

    /** @param list<string> $roles @param array<string, mixed> $extra non-sensitive: a JWT is signed, not encrypted */
    public function issuePair(string $subject, array $roles = [], array $extra = []): TokenPair
    {
        $now = $this->now();
        [$access, $accessExp] = $this->sign(TokenType::Access, $subject, $roles, $extra, $now, $this->accessTtl);
        [$refresh, $refreshExp] = $this->sign(TokenType::Refresh, $subject, [], [], $now, $this->refreshTtl);

        return new TokenPair($access, $refresh, $this->accessTtl, $accessExp, $refreshExp);
    }

    /**
     * @throws AppException Unauthorized (code token_expired | invalid_token | token_revoked) or Unavailable
     *                      when the revocation store cannot be reached (fail closed)
     */
    public function verify(string $token, TokenType $want = TokenType::Access): Claims
    {
        $now = $this->now();
        $keys = $this->verificationKeys();
        $prevLeeway = FirebaseJwt::$leeway;
        $prevTs = FirebaseJwt::$timestamp;
        FirebaseJwt::$leeway = $this->leeway;
        FirebaseJwt::$timestamp = $now;
        try {
            /** @var stdClass $p */
            $p = FirebaseJwt::decode($token, $keys);
        } catch (ExpiredException $e) {
            throw new AppException(Kind::Unauthorized, 'token_expired', 'token expired', [], $e);
        } catch (Throwable $e) {
            throw new AppException(Kind::Unauthorized, 'invalid_token', 'invalid token', [], $e);
        } finally {
            FirebaseJwt::$leeway = $prevLeeway;
            FirebaseJwt::$timestamp = $prevTs;
        }

        $invalid = static fn (string $why) => new AppException(Kind::Unauthorized, 'invalid_token', 'invalid token', [], new \RuntimeException($why));
        if (($p->iss ?? null) !== $this->issuer) {
            throw $invalid('issuer');
        }
        $aud = (array) ($p->aud ?? []);
        if (! in_array($this->audience, $aud, true)) {
            throw $invalid('audience');
        }
        if (($p->typ ?? null) !== $want->value) {
            throw $invalid("token type, want {$want->value}");
        }
        if (! isset($p->sub) || ! is_string($p->sub) || $p->sub === '' || ! isset($p->exp)) {
            throw $invalid('subject/expiry');
        }

        $claims = new Claims(
            $p->sub,
            array_values(array_map('strval', (array) ($p->roles ?? []))),
            (array) json_decode((string) json_encode($p->ext ?? new stdClass), true),
            $want,
            (string) ($p->jti ?? ''),
            (int) ($p->iat ?? 0),
            (int) $p->exp,
        );

        if ($this->revocations !== null) {
            try {
                $revoked = $this->revocations->isRevoked($claims);
            } catch (AppException $e) {
                throw $e;
            } catch (Throwable $e) {
                throw AppException::unavailable('cannot verify the token status right now', 'revocation_check_failed', $e);
            }
            if ($revoked) {
                throw AppException::unauthorized('token revoked', 'token_revoked');
            }
        }

        return $claims;
    }

    /** @return array{string, int} */
    private function sign(TokenType $type, string $subject, array $roles, array $extra, int $now, int $ttl): array
    {
        $k = $this->keys->signer();
        if (! $k->canSign()) {
            throw new \LogicException('this key set cannot sign tokens (verify-only)');
        }
        $exp = $now + $ttl;
        $payload = [
            'iss' => $this->issuer, 'sub' => $subject, 'aud' => [$this->audience],
            'iat' => $now, 'nbf' => $now, 'exp' => $exp, 'jti' => bin2hex(random_bytes(16)), 'typ' => $type->value,
        ];
        if ($roles !== []) {
            $payload['roles'] = array_values($roles);
        }
        if ($extra !== []) {
            $payload['ext'] = $extra;
        }

        return [FirebaseJwt::encode($payload, $k->signMaterial, $k->alg, $k->id), $exp];
    }

    /** @return array<string, FirebaseKey> */
    private function verificationKeys(): array
    {
        $out = [];
        foreach ($this->keys->ids() as $id) {
            $k = $this->keys->get($id);
            $out[$id] = new FirebaseKey($k->verifyMaterial, $k->alg);
        }

        return $out;
    }

    private function now(): int
    {
        return $this->clock ? (int) ($this->clock)() : time();
    }
}
