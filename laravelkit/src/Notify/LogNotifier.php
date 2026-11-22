<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

use Psr\Log\LoggerInterface;

/** Local development: logs instead of sending (masked recipients, never the body). */
final class LogNotifier implements Notifier
{
    public function __construct(private readonly LoggerInterface $log) {}

    public function sendEmail(Email $email): void
    {
        $email->validate();
        $this->log->info('email (not sent, log driver)', ['to' => array_map(Mask::email(...), $email->to), 'subject' => $email->subject]);
    }

    public function sendSms(Sms $sms): void
    {
        $sms->validate();
        $this->log->info('sms (not sent, log driver)', ['to' => Mask::phone($sms->to), 'chars' => mb_strlen($sms->body)]);
    }
}
