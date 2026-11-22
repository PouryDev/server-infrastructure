<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Notify;

use RuntimeException;

/** Unfixable delivery error: not retried, in the async path the job fails at once. */
final class PermanentFailure extends RuntimeException {}
