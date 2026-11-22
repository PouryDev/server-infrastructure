<?php

// Minimal OTLP/HTTP collector stand-in: appends "<path> <content-type> <bytes>" per POST to $LK_COLLECTOR_LOG and answers 200.
$log = getenv('LK_COLLECTOR_LOG') ?: '/tmp/collector.log';
$body = file_get_contents('php://input');
file_put_contents($log, sprintf("%s %s %d %s\n", $_SERVER['REQUEST_URI'], $_SERVER['CONTENT_TYPE'] ?? '', strlen($body), bin2hex(substr($body, 0, 0))), FILE_APPEND);
file_put_contents($log.'.last'.str_replace('/', '_', $_SERVER['REQUEST_URI']), $body);
header('Content-Type: application/x-protobuf');
http_response_code(200);
