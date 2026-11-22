<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Unit;

use Firebase\JWT\JWT as FJwt;
use Illuminate\Cache\ArrayStore;
use Illuminate\Cache\Repository;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Route;
use PouryDev\LaravelKit\Auth\Jwt;
use PouryDev\LaravelKit\Auth\Key;
use PouryDev\LaravelKit\Auth\KeySet;
use PouryDev\LaravelKit\Auth\PasswordHasher;
use PouryDev\LaravelKit\Auth\Revocations;
use PouryDev\LaravelKit\Auth\TokenType;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Errors\Kind;
use PouryDev\LaravelKit\Tests\TestCase;

final class AuthTest extends TestCase
{
    private int $now = 1_800_000_000;

    private function jwt(?KeySet $keys = null, ?Revocations $rev = null, string $aud = 'api', int $access = 900): Jwt
    {
        return new Jwt($keys ?? new KeySet(Key::ed25519('k1', Key::generateEd25519()['private'])), 'iss', $aud, $access, 3600, 0, $rev, fn () => $this->now);
    }

    private function code(callable $f): string
    {
        try {
            $f();
        } catch (AppException $e) {
            return $e->errorCode;
        }

        return 'none';
    }

    public function test_issue_and_verify_roundtrip_for_both_algorithms(): void
    {
        foreach ([new KeySet(Key::ed25519('e', Key::generateEd25519()['private'])), new KeySet(Key::hmac('h', str_repeat('s', 40)))] as $keys) {
            $j = $this->jwt($keys);
            $pair = $j->issuePair('user-1', ['admin'], ['tenant' => 't1']);
            $c = $j->verify($pair->accessToken);
            $this->assertSame('user-1', $c->subject);
            $this->assertSame(['admin'], $c->roles);
            $this->assertSame(['tenant' => 't1'], $c->extra);
            $this->assertTrue($c->hasAnyRole('x', 'admin'));
            $this->assertSame([], $j->verify($pair->refreshToken, TokenType::Refresh)->roles); // refresh carries identity only
            $this->assertSame(900, $pair->toArray()['expires_in']);
        }
    }

    public function test_access_and_refresh_tokens_are_not_interchangeable(): void
    {
        $j = $this->jwt();
        $p = $j->issuePair('u');
        $this->assertSame('invalid_token', $this->code(fn () => $j->verify($p->refreshToken)));
        $this->assertSame('invalid_token', $this->code(fn () => $j->verify($p->accessToken, TokenType::Refresh)));
    }

    public function test_expiry_has_its_own_code_so_clients_know_to_refresh(): void
    {
        $j = $this->jwt();
        $t = $j->issuePair('u')->accessToken;
        $this->now += 901;
        $this->assertSame('token_expired', $this->code(fn () => $j->verify($t)));
    }

    public function test_tampering_wrong_audience_and_unknown_key_are_invalid(): void
    {
        $j = $this->jwt();
        $t = $j->issuePair('u')->accessToken;
        [$h, $p, $s] = explode('.', $t);
        $this->assertSame('invalid_token', $this->code(fn () => $j->verify("$h.$p.".strrev($s))));
        $this->assertSame('invalid_token', $this->code(fn () => $j->verify('garbage')));
        $this->assertSame('invalid_token', $this->code(fn () => $this->jwt(null, null, 'other')->verify($t)), 'other key set');
        $keys = new KeySet(Key::ed25519('k1', Key::generateEd25519()['private']));
        $a = $this->jwt($keys, null, 'api');
        $tok = $a->issuePair('u')->accessToken;
        $this->assertSame('invalid_token', $this->code(fn () => $this->jwt($keys, null, 'wrong-audience')->verify($tok)));
    }

    public function test_algorithm_confusion_is_blocked(): void
    {
        $pair = Key::generateEd25519();
        $keys = new KeySet(Key::ed25519Public('k1', $pair['public']));
        // attacker signs HS256 using the PUBLIC key as the secret, with the right kid
        $forged = FJwt::encode(['iss' => 'iss', 'sub' => 'admin', 'aud' => ['api'], 'iat' => $this->now, 'exp' => $this->now + 900, 'typ' => 'access'], str_pad($pair['public'], 32, 'x'), 'HS256', 'k1');
        $this->assertSame('invalid_token', $this->code(fn () => $this->jwt($keys)->verify($forged)));
        $none = rtrim(strtr(base64_encode('{"alg":"none","kid":"k1"}'), '+/', '-_'), '=').'.'.rtrim(strtr(base64_encode('{"iss":"iss","sub":"x","aud":["api"],"exp":'.($this->now + 900).',"typ":"access"}'), '+/', '-_'), '=').'.';
        $this->assertSame('invalid_token', $this->code(fn () => $this->jwt($keys)->verify($none)));
    }

    public function test_verify_only_key_set_cannot_sign_and_verifies_tokens_of_the_issuer(): void
    {
        $pair = Key::generateEd25519();
        $token = $this->jwt(new KeySet(Key::ed25519('k1', $pair['private'])))->issuePair('u', ['r'])->accessToken;
        $verifier = $this->jwt(new KeySet(Key::ed25519Public('k1', $pair['public'])));
        $this->assertSame('u', $verifier->verify($token)->subject);
        $this->expectException(\LogicException::class);
        $verifier->issuePair('u');
    }

    public function test_pem_and_raw_formats_are_interchangeable_so_go_and_laravel_services_interoperate(): void
    {
        $k = Key::generateEd25519();
        $viaPem = $this->jwt(new KeySet(Key::ed25519('k1', $k['private_pem'])))->issuePair('u')->accessToken;
        $this->assertSame('u', $this->jwt(new KeySet(Key::ed25519Public('k1', $k['public_pem'])))->verify($viaPem)->subject);
        $this->assertSame('u', $this->jwt(new KeySet(Key::ed25519Public('k1', $k['public'])))->verify($viaPem)->subject);
        $oneLine = str_replace("\n", '\n', trim($k['private_pem'])); // as stored in a .env file
        $this->assertSame('u', $this->jwt(new KeySet(Key::ed25519('k1', $oneLine)))->verify($viaPem)->subject);
    }

    public function test_rotation_new_key_signs_old_key_still_verifies(): void
    {
        $old = Key::ed25519('k1', Key::generateEd25519()['private']);
        $new = Key::ed25519('k2', Key::generateEd25519()['private']);
        $oldToken = $this->jwt(new KeySet($old))->issuePair('u')->accessToken;
        $rotated = $this->jwt(new KeySet($new, $old));
        $this->assertSame('u', $rotated->verify($oldToken)->subject);
        $this->assertSame('u', $rotated->verify($rotated->issuePair('u')->accessToken)->subject);
        $this->assertSame('invalid_token', $this->code(fn () => $this->jwt(new KeySet($new))->verify($oldToken)));
    }

    public function test_revocation_of_one_token_and_of_a_subject(): void
    {
        $rev = new Revocations(new Repository(new ArrayStore), 3600, fn () => $this->now);
        $j = $this->jwt(null, $rev);
        $a = $j->issuePair('u1')->accessToken;
        $b = $j->issuePair('u1')->accessToken;
        $other = $j->issuePair('u2')->accessToken;
        $rev->revoke($j->verify($a));
        $this->assertSame('token_revoked', $this->code(fn () => $j->verify($a)));
        $this->assertSame('u1', $j->verify($b)->subject);

        $rev->revokeSubject('u1');
        $this->assertSame('token_revoked', $this->code(fn () => $j->verify($b)));
        $this->assertSame('u2', $j->verify($other)->subject);
        $this->now += 1;
        $this->assertSame('u1', $j->verify($j->issuePair('u1')->accessToken)->subject, 'tokens issued after the logout work');
    }

    public function test_a_broken_revocation_store_fails_closed_with_503(): void
    {
        $broken = new class extends Repository
        {
            public function __construct()
            {
                parent::__construct(new ArrayStore);
            }

            public function many(array $keys): array
            {
                throw new \RuntimeException('redis down');
            }
        };
        $j = $this->jwt(null, new Revocations($broken));
        $t = $j->issuePair('u')->accessToken;
        try {
            $j->verify($t);
            $this->fail();
        } catch (AppException $e) {
            $this->assertSame(Kind::Unavailable, $e->kind);
            $this->assertSame('revocation_check_failed', $e->errorCode);
        }
    }

    public function test_hs256_secret_must_be_long_enough(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        Key::hmac('k', 'short');
    }

    public function test_password_hasher_uses_argon2id_and_upgrades_old_hashes(): void
    {
        $h = new PasswordHasher;
        $argon = $h->hash('s3cret');
        $this->assertStringStartsWith('$argon2id$', $argon);
        $this->assertSame([true, false], $h->verify('s3cret', $argon));
        $this->assertSame([false, false], $h->verify('wrong', $argon));
        $this->assertSame([true, true], $h->verify('s3cret', password_hash('s3cret', PASSWORD_BCRYPT)), 'legacy bcrypt hash verifies and asks for a rehash');
        $this->assertSame([false, false], $h->verify('x', ''));
    }

    // --- guard + middleware through real routes

    protected function defineRoutes($router): void
    {
        Route::middleware('kit.auth')->get('api/me', fn (Request $r) => ['sub' => auth()->user()->id(), 'roles' => auth()->user()->roles()]);
        Route::middleware(['kit.auth', 'kit.roles:admin,root'])->get('api/admin', fn () => ['ok' => true]);
        Route::middleware('auth:jwt')->get('api/plain', fn () => ['sub' => auth()->id()]);
    }

    public function test_middleware_guard_and_roles_over_http(): void
    {
        $j = $this->app->make(Jwt::class);
        $admin = $j->issuePair('a1', ['admin'])->accessToken;
        $viewer = $j->issuePair('v1', ['viewer'])->accessToken;
        $refresh = $j->issuePair('v1')->refreshToken;

        $this->getJson('api/me')->assertStatus(401)->assertJsonPath('error.code', 'missing_token')->assertHeader('WWW-Authenticate', 'Bearer realm="api"');
        $this->getJson('api/me', ['Authorization' => "Bearer $admin"])->assertOk()->assertJson(['sub' => 'a1', 'roles' => ['admin']]);
        $this->getJson('api/me', ['Authorization' => "Bearer $refresh"])->assertStatus(401)->assertJsonPath('error.code', 'invalid_token');
        $this->getJson('api/admin', ['Authorization' => "Bearer $viewer"])->assertStatus(403)->assertJsonPath('error.code', 'forbidden');
        $this->getJson('api/admin', ['Authorization' => "Bearer $admin"])->assertOk();
        $this->getJson('api/plain', ['Authorization' => "Bearer $viewer"])->assertOk()->assertJson(['sub' => 'v1']);
        $this->getJson('api/plain', ['Authorization' => 'Bearer nope'])->assertStatus(401);
    }
}
