<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

/** Recipients are personal data: logs carry masked ones (`0912***6789`, `a***@example.com`), never bodies. */
final class Mask
{
    public static function phone(string $p): string
    {
        $n = strlen($p);

        return $n <= 8 ? str_repeat('*', $n) : substr($p, 0, 4).'***'.substr($p, -4);
    }

    public static function email(string $e): string
    {
        [$local, $domain] = array_pad(explode('@', $e, 2), 2, '');

        return $domain === '' ? '***' : substr($local, 0, 1).'***@'.$domain;
    }
}
