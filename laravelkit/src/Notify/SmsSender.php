<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

/** A provider. Throw {@see PermanentFailure} for errors that retrying cannot fix (bad credentials, rejected number). */
interface SmsSender
{
    public function name(): string;

    public function send(Sms $sms, int $timeoutSeconds): void;
}
