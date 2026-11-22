<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Console;

use Illuminate\Console\Command;
use PouryDev\LaravelKit\Auth\Key;

final class JwtKeyCommand extends Command
{
    protected $signature = 'laravelkit:jwt-key {--hs256 : print a shared secret instead of an Ed25519 key pair}';

    protected $description = 'Print new JWT key material for .env (compatible with gokit\'s `jwt-key`)';

    public function handle(): int
    {
        if ($this->option('hs256')) {
            $this->line('JWT_SECRET='.base64_encode(random_bytes(48)));
            $this->newLine();
            $this->comment('One secret signs and verifies: every service that verifies tokens can also forge them. Prefer the Ed25519 pair.');

            return self::SUCCESS;
        }
        $k = Key::generateEd25519();
        $this->line('JWT_PRIVATE_KEY="'.str_replace("\n", '\n', trim($k['private_pem'])).'"');
        $this->line('JWT_PUBLIC_KEY="'.str_replace("\n", '\n', trim($k['public_pem'])).'"');
        $this->newLine();
        $this->comment('The issuing service gets JWT_PRIVATE_KEY (it verifies with it too). Services that only check tokens get JWT_PUBLIC_KEY only.');

        return self::SUCCESS;
    }
}
