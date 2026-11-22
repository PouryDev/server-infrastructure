<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Unit;

use PouryDev\LaravelKit\Tests\TestCase;

final class InstallCommandTest extends TestCase
{
    private string $example;

    protected function setUp(): void
    {
        parent::setUp();
        $this->example = base_path('.env.example');
    }

    protected function tearDown(): void
    {
        @unlink($this->example);
        @unlink(config_path('laravelkit.php'));
        parent::tearDown();
    }

    public function test_it_appends_only_the_variables_the_project_does_not_set_yet(): void
    {
        file_put_contents($this->example, "APP_NAME=Shop\nCACHE_STORE=redis\nLOG_CHANNEL=stack\n");
        $this->artisan('laravelkit:install')->assertSuccessful();

        $env = (string) file_get_contents($this->example);
        $this->assertSame(1, substr_count($env, 'CACHE_STORE='), 'dotenv keeps the first value of a repeated key: never add a second one');
        $this->assertSame(1, substr_count($env, 'LOG_CHANNEL='));
        $this->assertStringContainsString('LOG_CHANNEL=stack', $env, 'the project\'s own value is untouched');
        $this->assertStringContainsString('JWT_PRIVATE_KEY=', $env);
        $this->assertStringContainsString('NOTIFY_DRIVER=sync', $env);
    }

    public function test_running_it_twice_changes_nothing(): void
    {
        file_put_contents($this->example, "APP_NAME=Shop\n");
        $this->artisan('laravelkit:install')->assertSuccessful();
        $once = (string) file_get_contents($this->example);
        $this->artisan('laravelkit:install')->assertSuccessful();
        $this->assertSame($once, (string) file_get_contents($this->example));
    }
}
