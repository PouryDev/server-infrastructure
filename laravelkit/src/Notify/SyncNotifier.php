<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

use Illuminate\Support\Facades\Mail;
use OpenTelemetry\API\Trace\SpanKind;
use OpenTelemetry\API\Trace\StatusCode;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Errors\Kind;
use PouryDev\LaravelKit\Telemetry\Telemetry;
use Psr\Log\LoggerInterface;
use Throwable;

/**
 * Sync driver: returns after the provider accepted the message. Transient failures are retried with
 * exponential backoff (Laravel's `retry()` helper); {@see PermanentFailure} is not retried.
 *
 * Errors: `delivery_rejected` (Invalid: the provider refused the message) or `delivery_failed` (Unavailable).
 * Logs carry masked recipients, message id, provider and attempts, never the body (OTP codes, reset links).
 */
final class SyncNotifier implements Notifier
{
    /** @param int $attempts total tries @param int $timeout seconds per attempt (SMS HTTP call) */
    public function __construct(
        private readonly SmsSender $sms,
        private readonly LoggerInterface $log,
        private readonly Telemetry $telemetry,
        private readonly int $attempts = 3,
        private readonly int $timeout = 15,
        private readonly int $baseDelayMs = 200,
    ) {}

    /** The same notifier with one attempt per call (for queue workers, where the queue does the retrying). */
    public function singleAttempt(): self
    {
        return new self($this->sms, $this->log, $this->telemetry, 1, $this->timeout, $this->baseDelayMs);
    }

    public function sendEmail(Email $email): void
    {
        $email->validate();
        $email = $email->withId();
        $this->deliver('email', 'mailer', $email->id, count($email->to), array_map(Mask::email(...), $email->to), function () use ($email) {
            Mail::to($email->to)->send(new KitMail($email));
        });
    }

    public function sendSms(Sms $sms): void
    {
        $sms->validate();
        $sms = $sms->withId();
        $this->deliver('sms', $this->sms->name(), $sms->id, 1, [Mask::phone($sms->to)], fn () => $this->sms->send($sms, $this->timeout));
    }

    /** @param list<string> $masked @param callable(): void $attempt */
    private function deliver(string $channel, string $provider, string $id, int $recipients, array $masked, callable $attempt): void
    {
        $span = $this->telemetry->tracer()?->spanBuilder("notify.send $channel")->setSpanKind(SpanKind::KIND_CLIENT)
            ->setAttribute('notify.channel', $channel)->setAttribute('notify.provider', $provider)
            ->setAttribute('notify.recipients', $recipients)->startSpan();
        $scope = $span?->activate();
        $tries = 0;
        $start = hrtime(true);
        try {
            $this->retrying(function () use (&$tries, $attempt, $channel) {
                $tries++;
                $s = $this->telemetry->tracer()?->spanBuilder("notify.attempt $channel")->setAttribute('notify.attempt', $tries)->startSpan();
                try {
                    $attempt();
                } catch (Throwable $e) {
                    $s?->recordException($e);
                    $s?->setStatus(StatusCode::STATUS_ERROR);
                    throw $e;
                } finally {
                    $s?->end();
                }
            });
            $this->telemetry->count('notify.deliveries', ['channel' => $channel, 'provider' => $provider, 'outcome' => 'sent']);
            $this->log->info('notification sent', ['channel' => $channel, 'provider' => $provider, 'id' => $id, 'to' => $masked, 'attempts' => $tries, 'ms' => round((hrtime(true) - $start) / 1e6, 1)]);
        } catch (Throwable $e) {
            $permanent = $e instanceof PermanentFailure;
            $outcome = $permanent ? 'rejected' : 'failed';
            $span?->recordException($e);
            $span?->setStatus(StatusCode::STATUS_ERROR, $outcome);
            $this->telemetry->count('notify.deliveries', ['channel' => $channel, 'provider' => $provider, 'outcome' => $outcome]);
            $this->log->warning('notification failed', ['channel' => $channel, 'provider' => $provider, 'id' => $id, 'to' => $masked, 'attempts' => $tries, 'outcome' => $outcome, 'error' => $e->getMessage()]);

            throw $permanent
                ? new AppException(Kind::Invalid, 'delivery_rejected', 'the provider rejected the message', [], $e)
                : new AppException(Kind::Unavailable, 'delivery_failed', 'could not deliver the message right now', [], $e);
        } finally {
            $scope?->detach();
            $span?->end();
        }
    }

    private function retrying(callable $fn): void
    {
        $n = 0;
        while (true) {
            try {
                $fn();

                return;
            } catch (Throwable $e) {
                if ($e instanceof PermanentFailure || ++$n >= $this->attempts) {
                    throw $e;
                }
                $delay = $this->baseDelayMs * (2 ** ($n - 1));
                usleep(random_int((int) ($delay * 500), $delay * 1000)); // jittered backoff
            }
        }
    }
}
