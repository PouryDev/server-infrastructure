<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Http;

use Illuminate\Http\JsonResponse;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Redis;

/**
 * `/healthz`: the process is alive; checks nothing (the compose healthcheck and the uptime probe use it, and
 * a database blip must not restart the container). `/readyz`: can this instance serve traffic right now.
 */
final class HealthController
{
    public function live(): JsonResponse
    {
        return new JsonResponse(['status' => 'ok'], 200, ['Cache-Control' => 'no-store']);
    }

    public function ready(): JsonResponse
    {
        $failed = [];
        foreach ((array) config('laravelkit.http.ready_checks', ['database', 'redis']) as $check) {
            try {
                match ($check) {
                    'database' => DB::select('select 1'),
                    'redis' => Redis::connection()->ping(),
                    default => null,
                };
            } catch (\Throwable $e) {
                report($e);
                $failed[] = $check;
            }
        }

        return new JsonResponse(
            $failed === [] ? ['status' => 'ok'] : ['status' => 'unavailable', 'failed' => $failed],
            $failed === [] ? 200 : 503,
            ['Cache-Control' => 'no-store'],
        );
    }
}
