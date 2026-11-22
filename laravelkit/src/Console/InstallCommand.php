<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Console;

use Illuminate\Console\Command;
use Illuminate\Filesystem\Filesystem;

final class InstallCommand extends Command
{
    protected $signature = 'laravelkit:install {--force : overwrite the published config}';

    protected $description = 'Publish config/laravelkit.php and add the kit\'s variables to .env.example';

    private const ENV = <<<'ENV'

# --- laravelkit ---
# infra contract: Redis keys must start with REDIS_KEY_PREFIX (Laravel applies the prefix itself)
REDIS_CLIENT=phpredis
REDIS_PREFIX=${REDIS_KEY_PREFIX}
CACHE_STORE=redis
CACHE_PREFIX=${REDIS_KEY_PREFIX}
LOG_CHANNEL=kit
LOG_STDERR_FORMATTER=
# stateless JWT (php artisan laravelkit:jwt-key)
JWT_ISSUER=
JWT_AUDIENCE=
JWT_PRIVATE_KEY=
JWT_PUBLIC_KEY=
JWT_ACCESS_TTL=900
JWT_REFRESH_TTL=2592000
# set to "redis" to make tokens revocable (logout, log out everywhere)
JWT_REVOCATION_STORE=
# telemetry: OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_SERVICE_NAME come from the infra env file
NOTIFY_DRIVER=sync
SMS_PROVIDER=log
KAVENEGAR_API_KEY=
KAVENEGAR_SENDER=
ENV;

    public function handle(Filesystem $files): int
    {
        $this->callSilent('vendor:publish', ['--tag' => 'laravelkit-config', '--force' => (bool) $this->option('force')]);
        $this->info('Published config/laravelkit.php');

        $example = base_path('.env.example');
        if ($files->exists($example)) {
            $current = $files->get($example);
            // dotenv keeps the FIRST value of a repeated key, so a key the project already sets is never appended
            $missing = [];
            foreach (explode("\n", self::ENV) as $line) {
                if (preg_match('/^([A-Z][A-Z0-9_]*)=/', $line, $m) === 1 && preg_match('/^'.preg_quote($m[1], '/').'=/m', $current) === 1) {
                    continue;
                }
                $missing[] = $line;
            }
            $block = rtrim(implode("\n", $missing));
            if (str_contains($current, '# --- laravelkit ---')) {
                $this->line('.env.example already has the laravelkit section');
            } elseif (preg_match('/^[A-Z]/m', $block) === 1) {
                $files->append($example, $block."\n");
                $this->info('Added the missing laravelkit variables to .env.example');
            }
        }

        $this->newLine();
        $this->line('Next steps:');
        $this->line('  1. php artisan laravelkit:jwt-key            and put the output in the environment');
        $this->line('  2. add to config/auth.php guards:            \'jwt\' => [\'driver\' => \'jwt\', \'provider\' => null]  (optional: the kit adds it when missing)');
        $this->line('  3. protect routes with                       kit.auth  and  kit.roles:admin');
        $this->line('  4. see the README for filtering, cache and notify');

        return self::SUCCESS;
    }
}
