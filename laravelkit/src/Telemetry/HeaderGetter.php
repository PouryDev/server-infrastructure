<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Telemetry;

use OpenTelemetry\Context\Propagation\PropagationGetterInterface;

/** Reads propagation headers from Symfony's header bag (`['traceparent' => ['00-...']]`). */
final class HeaderGetter implements PropagationGetterInterface
{
    public function keys($carrier): array
    {
        return array_keys($carrier);
    }

    public function get($carrier, string $key): ?string
    {
        $v = $carrier[strtolower($key)] ?? null;

        return is_array($v) ? ($v[0] ?? null) : $v;
    }
}
