<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Logging;

use Monolog\Formatter\JsonFormatter;
use Monolog\Handler\StreamHandler;
use Monolog\Logger;

/**
 * Channel `kit`: one JSON object per line on stderr (the infra reads container logs, there is no log file),
 * with trace_id / span_id. Registered by the service provider; select with LOG_CHANNEL=kit.
 */
final class JsonStderrChannel
{
    /** @param array<string, mixed> $config */
    public function __invoke(array $config): Logger
    {
        $handler = new StreamHandler('php://stderr', $config['level'] ?? 'debug');
        $handler->setFormatter(new JsonFormatter(JsonFormatter::BATCH_MODE_NEWLINES, true));

        return new Logger('app', [$handler], [new TraceContextProcessor]);
    }
}
