<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

use Illuminate\Bus\Queueable;
use Illuminate\Contracts\Queue\ShouldQueue;
use Illuminate\Foundation\Bus\Dispatchable;
use Illuminate\Queue\InteractsWithQueue;
use Illuminate\Queue\SerializesModels;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Errors\Kind;

/**
 * The async driver's job. One attempt per run here: retries and the dead-letter table are Laravel's own
 * (`tries`, `backoff`, `failed_jobs`), so `queue:work` and `queue:retry` work as usual. The worker delivers
 * through the sync driver with a single attempt; permanent failures fail the job at once (no retries).
 */
final class DeliverNotification implements ShouldQueue
{
    use Dispatchable, InteractsWithQueue, Queueable, SerializesModels;

    public int $tries;

    /** @var list<int> */
    public array $backoff = [10, 60, 300];

    /** @param array{kind: 'email'|'sms', data: array<string, mixed>} $payload */
    public function __construct(public readonly array $payload, int $tries = 3)
    {
        $this->tries = $tries;
    }

    public function handle(SyncNotifier $sync): void
    {
        $single = $sync->singleAttempt(); // the queue retries, not the notifier
        try {
            $this->payload['kind'] === 'email'
                ? $single->sendEmail(Email::fromArray($this->payload['data']))
                : $single->sendSms(Sms::fromArray($this->payload['data']));
        } catch (AppException $e) {
            if ($e->kind === Kind::Invalid) { // rejected or invalid: retrying cannot help
                $this->fail($e);

                return;
            }
            throw $e;
        }
    }
}
