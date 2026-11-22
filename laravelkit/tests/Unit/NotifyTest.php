<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Tests\Unit;

use Illuminate\Contracts\Bus\Dispatcher;
use Illuminate\Support\Facades\Bus;
use Illuminate\Support\Facades\Http;
use Illuminate\Support\Facades\Mail;
use Monolog\Handler\TestHandler;
use Monolog\Logger;
use PouryDev\LaravelKit\Errors\AppException;
use PouryDev\LaravelKit\Errors\Kind;
use PouryDev\LaravelKit\Notify\AsyncNotifier;
use PouryDev\LaravelKit\Notify\DeliverNotification;
use PouryDev\LaravelKit\Notify\Email;
use PouryDev\LaravelKit\Notify\KitMail;
use PouryDev\LaravelKit\Notify\Mask;
use PouryDev\LaravelKit\Notify\Notifier;
use PouryDev\LaravelKit\Notify\PermanentFailure;
use PouryDev\LaravelKit\Notify\Providers\Kavenegar;
use PouryDev\LaravelKit\Notify\Sms;
use PouryDev\LaravelKit\Notify\SmsSender;
use PouryDev\LaravelKit\Notify\SyncNotifier;
use PouryDev\LaravelKit\Telemetry\Telemetry;
use PouryDev\LaravelKit\Tests\TestCase;

final class FakeSms implements SmsSender
{
    /** @var list<Sms> */
    public array $sent = [];

    /** @var list<\Throwable> failures to throw, one per call, before succeeding */
    public array $failures = [];

    public int $calls = 0;

    public function name(): string
    {
        return 'fake';
    }

    public function send(Sms $sms, int $timeoutSeconds): void
    {
        $this->calls++;
        if ($this->failures !== []) {
            throw array_shift($this->failures);
        }
        $this->sent[] = $sms;
    }
}

final class NotifyTest extends TestCase
{
    private TestHandler $logs;

    private function sync(FakeSms $sms, int $attempts = 3): SyncNotifier
    {
        $this->logs = new TestHandler;

        return new SyncNotifier($sms, new Logger('t', [$this->logs]), Telemetry::disabled(), $attempts, 5, 1);
    }

    private function code(callable $f): string
    {
        try {
            $f();
        } catch (AppException $e) {
            return $e->errorCode;
        }

        return 'none';
    }

    public function test_validation_happens_before_anything_is_sent(): void
    {
        $sms = new FakeSms;
        $n = $this->sync($sms);
        foreach ([
            fn () => $n->sendSms(new Sms('12', 'x')),
            fn () => $n->sendSms(new Sms('09123456789', '  ')),
            fn () => $n->sendEmail(new Email(['not-an-email'], 'Hi', 'x')),
            fn () => $n->sendEmail(new Email(['a@example.com'], "Hi\r\nBcc: evil@example.com", 'x')),
            fn () => $n->sendEmail(new Email(['a@example.com'], 'Hi')),
            fn () => $n->sendEmail(new Email([], 'Hi', 'x')),
        ] as $f) {
            $this->assertSame('invalid_argument', $this->code($f));
        }
        $this->assertSame(0, $sms->calls);
    }

    public function test_sms_is_delivered_and_logs_are_masked_without_the_body(): void
    {
        $sms = new FakeSms;
        $this->sync($sms)->sendSms(new Sms('09123456789', 'Your code is 424242'));
        $this->assertCount(1, $sms->sent);
        $this->assertNotSame('', $sms->sent[0]->id, 'a stable id is assigned');
        $log = json_encode($this->logs->getRecords());
        $this->assertStringContainsString('0912***6789', $log);
        $this->assertStringNotContainsString('09123456789', $log);
        $this->assertStringNotContainsString('424242', $log);
    }

    public function test_transient_failures_are_retried_with_the_same_id(): void
    {
        $sms = new FakeSms;
        $sms->failures = [new \RuntimeException('timeout'), new \RuntimeException('502')];
        $this->sync($sms)->sendSms(new Sms('09123456789', 'hi'));
        $this->assertSame(3, $sms->calls);
        $this->assertCount(1, $sms->sent);
    }

    public function test_exhausted_retries_report_delivery_failed_503(): void
    {
        $sms = new FakeSms;
        $sms->failures = array_fill(0, 5, new \RuntimeException('down'));
        try {
            $this->sync($sms)->sendSms(new Sms('09123456789', 'hi'));
            $this->fail();
        } catch (AppException $e) {
            $this->assertSame([Kind::Unavailable, 'delivery_failed'], [$e->kind, $e->errorCode]);
        }
        $this->assertSame(3, $sms->calls);
    }

    public function test_permanent_failures_are_not_retried_and_are_reported_as_rejected(): void
    {
        $sms = new FakeSms;
        $sms->failures = [new PermanentFailure('bad credentials')];
        try {
            $this->sync($sms)->sendSms(new Sms('09123456789', 'hi'));
            $this->fail();
        } catch (AppException $e) {
            $this->assertSame([Kind::Invalid, 'delivery_rejected'], [$e->kind, $e->errorCode]);
        }
        $this->assertSame(1, $sms->calls);
    }

    public function test_email_goes_through_laravels_mailer(): void
    {
        Mail::fake();
        $this->sync(new FakeSms)->sendEmail(new Email(['ada@example.com'], 'Welcome', 'plain', '<p>html</p>', ['cc@example.com']));
        Mail::assertSent(KitMail::class, fn (KitMail $m) => $m->hasTo('ada@example.com') && $m->email->subject === 'Welcome' && $m->email->id !== '');
        $this->assertStringNotContainsString('ada@example.com', json_encode($this->logs->getRecords()));
    }

    public function test_the_mailable_renders_html_and_text_bodies(): void
    {
        $m = new KitMail(new Email(['a@example.com'], 'S', 'plain text', '<p>hello</p>'));
        $this->assertStringContainsString('<p>hello</p>', $m->render());
        $m->assertSeeInHtml('<p>hello</p>', false);
        $m->assertSeeInText('plain text');
    }

    public function test_async_driver_validates_then_queues_one_job_per_message(): void
    {
        Bus::fake();
        $n = new AsyncNotifier($this->app->make(Dispatcher::class), Telemetry::disabled(), 'notifications', null, 4);
        $this->assertSame('invalid_argument', $this->code(fn () => $n->sendSms(new Sms('bad', 'x'))));
        Bus::assertNothingDispatched();

        $n->sendSms(new Sms('09123456789', 'hi'));
        $n->sendEmail(new Email(['a@example.com'], 'S', 'b'));
        Bus::assertDispatchedTimes(DeliverNotification::class, 2);
        Bus::assertDispatched(DeliverNotification::class, fn ($j) => $j->queue === 'notifications' && $j->tries === 4 && $j->payload['kind'] === 'sms' && $j->payload['data']['id'] !== '');
    }

    public function test_the_job_delivers_once_with_the_queue_doing_the_retrying(): void
    {
        $sms = new FakeSms;
        $sms->failures = [new \RuntimeException('timeout')];
        $job = new DeliverNotification(['kind' => 'sms', 'data' => (new Sms('09123456789', 'hi', 'id-1'))->toArray()]);
        try {
            $job->handle($this->sync($sms, attempts: 3));
            $this->fail('transient failure must bubble up so the queue retries');
        } catch (AppException $e) {
            $this->assertSame(Kind::Unavailable, $e->kind);
        }
        $this->assertSame(1, $sms->calls, 'no internal retry in the worker');
        $job->handle($this->sync($sms));
        $this->assertSame('id-1', $sms->sent[0]->id, 'the id survives the queue');
    }

    public function test_the_default_container_driver_is_sync_with_the_log_sms_provider(): void
    {
        $this->assertInstanceOf(SyncNotifier::class, $this->app->make(Notifier::class));
        $this->app->make(Notifier::class)->sendSms(new Sms('09123456789', 'hi')); // log provider: nothing leaves the process
        $this->addToAssertionCount(1);
    }

    public function test_kavenegar_maps_responses_to_retryable_and_permanent(): void
    {
        $k = new Kavenegar('KEY123', '1000');
        Http::fake(['api.kavenegar.com/*' => Http::sequence()
            ->push(['return' => ['status' => 200]])
            ->push(['return' => ['status' => 418]])
            ->push(['return' => ['status' => 500]])
            ->push('', 500)]);
        $k->send(new Sms('09123456789', 'hi'), 5);
        try {
            $k->send(new Sms('09123456789', 'hi'), 5);
            $this->fail();
        } catch (PermanentFailure) {
        }
        try {
            $k->send(new Sms('09123456789', 'hi'), 5);
            $this->fail();
        } catch (\RuntimeException $e) {
            $this->assertNotInstanceOf(PermanentFailure::class, $e);
        }
        try {
            $k->send(new Sms('09123456789', 'hi'), 5);
            $this->fail();
        } catch (\RuntimeException $e) {
            $this->assertStringNotContainsString('KEY123', $e->getMessage());
        }
    }

    public function test_masks(): void
    {
        $this->assertSame('0912***6789', Mask::phone('09123456789'));
        $this->assertSame('a***@example.com', Mask::email('ada@example.com'));
        $this->assertSame('***', Mask::email('garbage'));
    }
}
