<?php

declare(strict_types=1);

namespace PouryDev\LaravelKit\Errors;

use Illuminate\Auth\Access\AuthorizationException;
use Illuminate\Auth\AuthenticationException;
use Illuminate\Database\Eloquent\ModelNotFoundException;
use Illuminate\Database\QueryException;
use Illuminate\Http\Exceptions\ThrottleRequestsException;
use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Validation\ValidationException;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\HttpKernel\Exception\HttpExceptionInterface;
use Throwable;

/**
 * Turns every exception into the same body the Go services return:
 * {"error": {"code", "message", "fields"?, "request_id"?}}. Server errors are logged with their cause and
 * shown to the client as a generic message.
 */
final class ErrorRenderer
{
    /** @param list<string> $paths */
    public function __construct(
        private readonly array $paths = ['api/*'],
        private readonly bool $alwaysJson = false,
        private readonly string $requestIdAttribute = 'laravelkit.request_id',
    ) {}

    public function wants(Request $request): bool
    {
        return $this->alwaysJson || $request->expectsJson() || $request->is(...$this->paths);
    }

    public function render(Throwable $e, Request $request): ?JsonResponse
    {
        if (! $this->wants($request)) {
            return null;
        }

        [$status, $code, $message, $fields, $headers] = $this->describe($e);

        $detail = ['code' => $code, 'message' => $message];
        if ($fields !== []) {
            $detail['fields'] = $fields;
        }
        if (($id = $request->attributes->get($this->requestIdAttribute)) !== null) {
            $detail['request_id'] = $id;
        }
        $headers['Cache-Control'] = 'no-store';
        if ($status === 401) {
            $headers['WWW-Authenticate'] = 'Bearer realm="api"';
        }
        if ($status === 503) {
            $headers['Retry-After'] = '1';
        }

        return new JsonResponse(['error' => $detail], $status, $headers, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES);
    }

    /** @return array{int, string, string, list<array{field: string, message: string}>, array<string, string>} */
    private function describe(Throwable $e): array
    {
        if ($e instanceof AppException) {
            $status = $e->kind->status();
            $safe = $status < 500 || $e->kind === Kind::Unavailable;

            return [$status, $safe ? $e->errorCode : Kind::Internal->value, $safe ? $e->getMessage() : 'internal error', $safe ? $e->fields : [], []];
        }
        if ($e instanceof ValidationException) {
            $fields = [];
            foreach ($e->errors() as $field => $messages) {
                foreach ($messages as $m) {
                    $fields[] = ['field' => (string) $field, 'message' => (string) $m];
                }
            }

            return [400, Kind::Invalid->value, 'invalid input', $fields, []];
        }
        if ($e instanceof AuthenticationException) {
            return [401, Kind::Unauthorized->value, 'unauthorized', [], []];
        }
        if ($e instanceof AuthorizationException) {
            return [403, Kind::Forbidden->value, 'forbidden', [], []];
        }
        if ($e instanceof ModelNotFoundException) {
            return [404, Kind::NotFound->value, class_basename($e->getModel()).' not found', [], []];
        }
        if ($e instanceof ThrottleRequestsException) {
            return [429, 'too_many_requests', 'too many requests', [], $e->getHeaders()];
        }
        if ($e instanceof HttpExceptionInterface) {
            $s = $e->getStatusCode();
            $kind = match (true) {
                $s === 401 => Kind::Unauthorized,
                $s === 403 => Kind::Forbidden,
                $s === 404 => Kind::NotFound,
                $s === 409 => Kind::Conflict,
                $s === 503 => Kind::Unavailable,
                $s >= 500 => Kind::Internal,
                default => Kind::Invalid,
            };
            $code = match ($s) {
                405 => 'method_not_allowed', 413 => 'body_too_large', 415 => 'unsupported_media_type',
                422 => Kind::Invalid->value, 429 => 'too_many_requests', default => $kind->value,
            };
            $msg = $s >= 500 ? 'internal error' : strtolower(Response::$statusTexts[$s] ?? 'error'); // never the exception text: it may name models or routes

            return [$s, $s >= 500 && $s !== 503 ? Kind::Internal->value : $code, $msg, [], $e->getHeaders()];
        }
        if ($e instanceof QueryException) {
            // unique violation / serialization failure are the client's business, everything else is ours
            $state = (string) ($e->errorInfo[0] ?? '');
            if ($state === '23505') {
                return [409, 'conflict', 'resource already exists', [], []];
            }
            if ($state === '23503') {
                return [409, 'conflict', 'resource is referenced by or references another resource', [], []];
            }
            if ($state === '40001' || $state === '40P01') {
                return [503, Kind::Unavailable->value, 'please retry', [], []];
            }
        }

        return [500, Kind::Internal->value, 'internal error', [], []];
    }
}
