<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Auth;

/** Separates access tokens from refresh tokens so one can never be used as the other. */
enum TokenType: string
{
    case Access = 'access';
    case Refresh = 'refresh';
}
