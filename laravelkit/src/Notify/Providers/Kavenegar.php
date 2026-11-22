<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify\Providers;

use Illuminate\Support\Facades\Http;
use PouryDev\LaravelKit\Notify\PermanentFailure;
use PouryDev\LaravelKit\Notify\Sms;
use PouryDev\LaravelKit\Notify\SmsSender;

/** Kavenegar. The API key is part of the URL path, so URLs are stripped from every error this provider raises. */
final class Kavenegar implements SmsSender
{
    public function __construct(private readonly string $apiKey, private readonly string $sender = '') {}

    public function name(): string
    {
        return 'kavenegar';
    }

    public function send(Sms $sms, int $timeoutSeconds): void
    {
        try {
            $res = Http::asForm()->timeout($timeoutSeconds)->post("https://api.kavenegar.com/v1/{$this->apiKey}/sms/send.json", array_filter([
                'receptor' => $sms->to, 'message' => $sms->body, 'sender' => $this->sender,
            ]));
        } catch (\Throwable $e) {
            throw new \RuntimeException('kavenegar request failed: '.get_class($e)); // no URL, no key
        }
        $status = (int) ($res->json('return.status') ?? $res->status());
        if ($status === 200) {
            return;
        }
        $msg = "kavenegar status $status";
        // 4xx = request problem (credentials 401/403, bad receptor 412/418, no credit 402...): retrying cannot help
        if (($status >= 400 && $status < 500) && $status !== 429) {
            throw new PermanentFailure($msg);
        }
        throw new \RuntimeException($msg);
    }
}
