#!/usr/bin/env python3
"""Initialize private manager credentials without echoing passwords or hashes."""
import getpass
import hashlib
import os
import re
from pathlib import Path
import secrets

destination = Path(__file__).resolve().parent.parent / "manager" / ".env"
if destination.exists():
    raise SystemExit("Manager credentials already exist; refusing to overwrite.")
username = input("Username: ").strip()
password = getpass.getpass("Password: ")
if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{1,63}", username) or len(password) < 8:
    raise SystemExit("Provide a username and a password of at least 8 characters.")
salt = secrets.token_bytes(16)
password_hash = hashlib.pbkdf2_hmac("sha256", password.encode(), salt, 600000).hex()
fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w") as stream:
    stream.write(f"MANAGER_USERNAME={username}\nMANAGER_PASSWORD_HASH={salt.hex()}:{password_hash}\nMANAGER_GATEWAY_TOKEN={secrets.token_hex(32)}\n")
print("Private manager credentials initialized.")
