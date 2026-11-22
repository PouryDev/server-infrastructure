<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

/**
 * What use cases depend on: SMS and email behind one interface. The driver (sync, async, log) is chosen in
 * configuration, never in the caller. Both drivers validate first and throw AppException::invalid, so a bad
 * message fails in the request, not later in the queue.
 */
interface Notifier
{
    public function sendEmail(Email $email): void;

    public function sendSms(Sms $sms): void;
}
