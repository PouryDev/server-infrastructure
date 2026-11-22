<?php

declare(strict_types=1);

/*
 * laravelkit configuration. Everything is driven by environment variables, the same image runs anywhere.
 * Publish with: php artisan vendor:publish --tag=laravelkit-config  (or php artisan laravelkit:install)
 */

return [
    'errors' => [
        // false = leave the project's own error bodies alone (a project with shipped clients that parse them).
        'enabled' => env('LARAVELKIT_ERRORS', true),
        // Render the uniform {"error": {...}} body for these requests. "api/*" paths and anything that asks
        // for JSON are matched; add patterns for APIs mounted elsewhere.
        'paths' => ['api/*', 'v1/*'],
        'always_json' => env('LARAVELKIT_ERRORS_ALWAYS_JSON', false),
    ],

    'http' => [
        // Adds X-Request-Id, a structured access log line (with trace_id) and no-store on API responses.
        'request_id_header' => 'X-Request-Id',
        'access_log' => env('LARAVELKIT_ACCESS_LOG', true),
        'access_log_channel' => env('LARAVELKIT_ACCESS_LOG_CHANNEL'), // null = default channel (stderr/stdout JSON)
        'skip_log_paths' => ['healthz', 'readyz'],
        // Register GET /healthz (liveness, no dependencies) and GET /readyz (database + redis).
        'global_middleware' => env('LARAVELKIT_GLOBAL_MIDDLEWARE', true), // request id + trace on every request, access log last
        'health_routes' => env('LARAVELKIT_HEALTH_ROUTES', true),
        'ready_checks' => ['database', 'redis'],
    ],

    'auth' => [
        'issuer' => env('JWT_ISSUER', env('APP_URL', 'laravel')),
        'audience' => env('JWT_AUDIENCE', env('APP_NAME', 'laravel')),
        'access_ttl' => (int) env('JWT_ACCESS_TTL', 900),            // seconds, 15 min
        'refresh_ttl' => (int) env('JWT_REFRESH_TTL', 2592000),      // seconds, 30 days
        'leeway' => (int) env('JWT_LEEWAY', 30),
        // Keys: "alg" is EdDSA (private+public) or HS256 (secret). The first key signs, all verify (rotation).
        // JWT_PRIVATE_KEY / JWT_PUBLIC_KEY / JWT_SECRET are base64. Generate with: php artisan laravelkit:jwt-key
        'key_id' => env('JWT_KEY_ID'), // null = "k1"
        'private_key' => env('JWT_PRIVATE_KEY'),
        'public_key' => env('JWT_PUBLIC_KEY'),
        'secret' => env('JWT_SECRET'),
        // Extra verification keys for rotation: [['id' => 'k0', 'public_key' => '...'] | ['id' => 'k0', 'secret' => '...']]
        'previous_keys' => [],
        // Revocable tokens: store ("redis" cache store) holding revocation marks. null = purely stateless.
        'revocation_store' => env('JWT_REVOCATION_STORE'),
        'guard' => 'jwt',
    ],

    'cache' => [
        // Cache store used by LaravelKit\Cache\KitCache; must be the shared Redis store in production.
        'store' => env('LARAVELKIT_CACHE_STORE'),                 // null = default store
        'namespace' => env('LARAVELKIT_CACHE_NAMESPACE', 'kit'),
        'jitter' => 0.1,                                          // +-10% on every stored TTL
        'max_ttl' => 86400,                                       // every key must expire; refuse anything longer
        'version_ttl' => 86400,                                   // TTL of the version counters
        'version_refresh' => 1,                                   // seconds a process trusts a version it read
    ],

    'telemetry' => [
        'enabled' => env('LARAVELKIT_TELEMETRY', true),
        // No endpoint = disabled. OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES are honoured by the SDK itself.
        'endpoint' => env('OTEL_EXPORTER_OTLP_ENDPOINT'),
        'protocol' => env('OTEL_EXPORTER_OTLP_PROTOCOL', 'http/protobuf'),
        'service_name' => env('OTEL_SERVICE_NAME', env('APP_NAME', 'laravel')),
        'metric_interval' => 30,                                  // seconds between metric exports per PHP worker
    ],

    'notify' => [
        'driver' => env('NOTIFY_DRIVER', 'sync'),                 // sync | async | log
        'queue' => env('NOTIFY_QUEUE', 'notifications'),
        'connection' => env('NOTIFY_QUEUE_CONNECTION'),
        'retries' => (int) env('NOTIFY_RETRIES', 3),
        'attempt_timeout' => (int) env('NOTIFY_ATTEMPT_TIMEOUT', 15),
        'sms' => [
            'provider' => env('SMS_PROVIDER', 'log'),             // kavenegar | twilio | log
            'kavenegar' => ['api_key' => env('KAVENEGAR_API_KEY'), 'sender' => env('KAVENEGAR_SENDER')],
            'twilio' => [
                'account_sid' => env('TWILIO_ACCOUNT_SID'),
                'auth_token' => env('TWILIO_AUTH_TOKEN'),
                'from' => env('TWILIO_FROM'),
            ],
        ],
    ],
];
