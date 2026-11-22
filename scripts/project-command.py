#!/usr/bin/env python3
"""SSH bridge to the destination's existing authenticated manager broker.

Executed inside manager-storefront via docker compose exec. Requests arrive on
stdin; neither app env values nor the broker credential appear in SSH arguments.
"""
import json
import os
import sys
import urllib.error
import urllib.request

operation = sys.argv[1] if len(sys.argv) == 2 else ''
paths = {'catalog': '/projects/catalog', 'preview': '/projects/preview', 'launch': '/projects/launch', 'job': '/projects/job'}
if operation not in paths:
    print(json.dumps({'error': 'Unsupported project operation'})); sys.exit(1)
body = sys.stdin.buffer.read(1024 * 1024 + 1)
if len(body) > 1024 * 1024:
    print(json.dumps({'error': 'Project request too large'})); sys.exit(1)
request = urllib.request.Request('http://127.0.0.1:8080' + paths[operation],
    data=body if operation != 'catalog' else None,
    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + os.environ.get('MANAGER_GATEWAY_TOKEN', '')})
try:
    with urllib.request.urlopen(request, timeout=45) as response: sys.stdout.buffer.write(response.read(2 * 1024 * 1024))
except urllib.error.HTTPError as exc:
    # Only the broker's bounded public error envelope is returned, never headers.
    sys.stdout.buffer.write(exc.read(4096))
except Exception:
    print(json.dumps({'error': 'Destination project runner unavailable'}))
