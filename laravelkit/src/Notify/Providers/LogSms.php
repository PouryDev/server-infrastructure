<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify\Providers;

use Illuminate\Support\Facades\Log;
use PouryDev\LaravelKit\Notify\Mask;
use PouryDev\LaravelKit\Notify\Sms;
use PouryDev\LaravelKit\Notify\SmsSender;

/** Local / tests: logs instead of sending (masked recipient, no body). */
final class LogSms implements SmsSender
{
    public function name(): string
    {
        return 'log';
    }

    public function send(Sms $sms, int $timeoutSeconds): void
    {
        Log::info('sms (not sent, log provider)', ['to' => Mask::phone($sms->to), 'id' => $sms->id, 'chars' => mb_strlen($sms->body)]);
    }
}
