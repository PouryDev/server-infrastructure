<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify\Providers;

use Illuminate\Support\Facades\Http;
use PouryDev\LaravelKit\Notify\PermanentFailure;
use PouryDev\LaravelKit\Notify\Sms;
use PouryDev\LaravelKit\Notify\SmsSender;

final class Twilio implements SmsSender
{
    public function __construct(private readonly string $accountSid, private readonly string $authToken, private readonly string $from) {}

    public function name(): string
    {
        return 'twilio';
    }

    public function send(Sms $sms, int $timeoutSeconds): void
    {
        $res = Http::asForm()->withBasicAuth($this->accountSid, $this->authToken)->timeout($timeoutSeconds)
            ->post("https://api.twilio.com/2010-04-01/Accounts/{$this->accountSid}/Messages.json", [
                'To' => $sms->to, 'From' => $this->from, 'Body' => $sms->body,
            ]);
        if ($res->successful()) {
            return;
        }
        $msg = 'twilio status '.$res->status().' code '.($res->json('code') ?? '?');
        if ($res->clientError() && $res->status() !== 429) {
            throw new PermanentFailure($msg);
        }
        throw new \RuntimeException($msg);
    }
}
