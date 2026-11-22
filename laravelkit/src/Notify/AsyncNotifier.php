<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

use Illuminate\Contracts\Bus\Dispatcher;
use OpenTelemetry\API\Trace\SpanKind;
use PouryDev\LaravelKit\Telemetry\Telemetry;

/**
 * Async driver: validates, puts the message on the Laravel queue (Redis, RabbitMQ, database... whatever the
 * project uses) and returns; `queue:work --queue=notifications` delivers it through {@see SyncNotifier}.
 * Delivery is at-least-once, so `Email::$id` / `Sms::$id` is stable for de-duplication downstream. The
 * queue's trace context is carried by Laravel's payload hook (see the service provider), so the worker's
 * spans belong to the request's trace.
 */
final class AsyncNotifier implements Notifier
{
    public function __construct(
        private readonly Dispatcher $bus,
        private readonly Telemetry $telemetry,
        private readonly string $queue = 'notifications',
        private readonly ?string $connection = null,
        private readonly int $tries = 3,
    ) {}

    public function sendEmail(Email $email): void
    {
        $email->validate();
        $email = $email->withId();
        $this->enqueue('email', $email->toArray(), $email->id);
    }

    public function sendSms(Sms $sms): void
    {
        $sms->validate();
        $sms = $sms->withId();
        $this->enqueue('sms', $sms->toArray(), $sms->id);
    }

    /** @param array<string, mixed> $data */
    private function enqueue(string $kind, array $data, string $id): void
    {
        $span = $this->telemetry->tracer()?->spanBuilder('notify.enqueue')->setSpanKind(SpanKind::KIND_PRODUCER)
            ->setAttribute('notify.channel', $kind)->setAttribute('notify.message_id', $id)->startSpan();
        $scope = $span?->activate();
        try {
            $job = (new DeliverNotification(['kind' => $kind, 'data' => $data], $this->tries))->onQueue($this->queue);
            if ($this->connection !== null) {
                $job->onConnection($this->connection);
            }
            $this->bus->dispatch($job);
            $this->telemetry->count('notify.enqueued', ['channel' => $kind]);
        } finally {
            $scope?->detach();
            $span?->end();
        }
    }
}
