<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Telemetry;

use Illuminate\Contracts\Foundation\Application;
use Illuminate\Database\Events\QueryExecuted;
use Illuminate\Queue\Events\JobExceptionOccurred;
use Illuminate\Queue\Events\JobFailed;
use Illuminate\Queue\Events\JobProcessed;
use Illuminate\Queue\Events\JobProcessing;
use Illuminate\Queue\Queue;
use Illuminate\Support\Facades\DB;
use Illuminate\Support\Facades\Event;
use Illuminate\Support\Facades\Http;
use OpenTelemetry\API\Trace\SpanInterface;
use OpenTelemetry\API\Trace\SpanKind;
use OpenTelemetry\API\Trace\StatusCode;
use OpenTelemetry\Context\ScopeInterface;
use Psr\Http\Message\RequestInterface;

/**
 * Child spans for what a request touches, all under the request's trace:
 *  - database queries (operation + parametrised SQL, never bindings);
 *  - outgoing HTTP calls get a `traceparent` header (so the callee continues the trace);
 *  - queue jobs carry the trace context in their payload and run in a consumer span.
 */
final class Instrumentation
{
    /** @var array<int, array{SpanInterface, ScopeInterface}> */
    private static array $jobs = [];

    public static function register(Application $app, Telemetry $t): void
    {
        if (! $t->enabled()) {
            return;
        }
        $tracer = $t->tracer();
        $prop = $t->propagator();

        DB::listen(static function (QueryExecuted $q) use ($tracer): void {
            $sql = ltrim($q->sql);
            $end = (int) (microtime(true) * 1e9);
            $span = $tracer->spanBuilder(strtoupper(strtok($sql, " \t\n(") ?: 'query').' '.($q->connectionName))
                ->setSpanKind(SpanKind::KIND_CLIENT)
                ->setStartTimestamp($end - (int) ($q->time * 1e6))
                ->setAttribute('db.system.name', $q->connection->getDriverName())
                ->setAttribute('db.query.text', mb_substr($sql, 0, 1000)) // placeholders only: bindings are data
                ->startSpan();
            $span->end($end);
        });

        Http::globalRequestMiddleware(static function (RequestInterface $request) use ($prop): RequestInterface {
            $carrier = [];
            $prop->inject($carrier);
            foreach ($carrier as $k => $v) {
                $request = $request->withHeader($k, $v);
            }

            return $request;
        });

        Queue::createPayloadUsing(static function () use ($prop): array {
            $carrier = [];
            $prop->inject($carrier);

            return $carrier === [] ? [] : ['laravelkit_trace' => $carrier];
        });

        Event::listen(JobProcessing::class, static function (JobProcessing $e) use ($tracer, $prop): void {
            $carrier = (array) ($e->job->payload()['laravelkit_trace'] ?? []);
            $parent = $prop->extract($carrier);
            $span = $tracer->spanBuilder('process '.$e->job->resolveName())->setParent($parent)
                ->setSpanKind(SpanKind::KIND_CONSUMER)->setAttribute('messaging.destination.name', $e->job->getQueue())
                ->setAttribute('messaging.system', 'laravel-queue')->startSpan();
            self::$jobs[spl_object_id($e->job)] = [$span, $span->activate()];
        });
        $end = static function (object $e, ?\Throwable $err = null) use ($t): void {
            $id = spl_object_id($e->job);
            if (! isset(self::$jobs[$id])) {
                return;
            }
            [$span, $scope] = self::$jobs[$id];
            unset(self::$jobs[$id]);
            if ($err !== null) {
                $span->recordException($err);
                $span->setStatus(StatusCode::STATUS_ERROR);
            }
            $scope->detach();
            $span->end();
            $t->flush();
        };
        Event::listen(JobProcessed::class, static fn (JobProcessed $e) => $end($e));
        Event::listen(JobFailed::class, static fn (JobFailed $e) => $end($e, $e->exception));
        Event::listen(JobExceptionOccurred::class, static fn (JobExceptionOccurred $e) => $end($e, $e->exception));
    }
}
