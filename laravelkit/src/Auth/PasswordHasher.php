<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

use Illuminate\Support\Facades\Hash;

/**
 * argon2id that upgrades itself: `verify()` accepts any hash PHP knows (bcrypt from an older Laravel app
 * included) and tells you when the stored hash should be replaced by `hash()` after a successful login.
 */
final class PasswordHasher
{
    public function hash(string $password): string
    {
        return Hash::driver('argon2id')->make($password);
    }

    /** @return array{bool, bool} [matches, needsRehash] */
    public function verify(string $password, string $hash): array
    {
        if ($hash === '' || ! password_verify($password, $hash)) {
            return [false, false];
        }

        return [true, Hash::driver('argon2id')->needsRehash($hash)];
    }
}
