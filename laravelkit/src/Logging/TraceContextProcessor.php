<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Logging;

use Monolog\LogRecord;
use Monolog\Processor\ProcessorInterface;
use OpenTelemetry\API\Trace\Span;

/** Adds `trace_id` / `span_id` of the active span so a log line can be matched to its trace in Grafana. */
final class TraceContextProcessor implements ProcessorInterface
{
    public function __invoke(LogRecord $record): LogRecord
    {
        if (! class_exists(Span::class)) {
            return $record;
        }
        $ctx = Span::getCurrent()->getContext();
        if (! $ctx->isValid()) {
            return $record;
        }

        return $record->with(extra: $record->extra + ['trace_id' => $ctx->getTraceId(), 'span_id' => $ctx->getSpanId()]);
    }
}
