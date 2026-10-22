#!/usr/bin/env python3
"""Passwort-Hash für AUTH_USERS, ohne Go.

Erzeugt dasselbe Format wie `go run ./cmd/passhash` (internal/auth/users.go):

    pbkdf2-sha256$<iterationen>$<salt>$<schlüssel>

PBKDF2-HMAC-SHA256, 16 Byte Salt, 32 Byte Schlüssel, beide Base64-URL ohne
"="-Padding. Nur Standardbibliothek.

Am Terminal wird das Passwort ohne Echo gelesen, sonst die erste Zeile von
stdin. Der Hash allein geht auf stdout, Hinweise auf stderr:

    python3 scripts/passhash.py
    printf '%s' "$PASSWORT" | python3 scripts/passhash.py
"""

import base64
import getpass
import hashlib
import secrets
import sys

ITERATIONS = 600000
SALT_LEN = 16
KEY_LEN = 32
PREFIX = "pbkdf2-sha256"


def b64(data):
    return base64.urlsafe_b64encode(data).rstrip(b"=").decode("ascii")


def read_password():
    if sys.stdin.isatty():
        return getpass.getpass("Passwort: ")

    return sys.stdin.readline().rstrip("\r\n")


def main():
    password = read_password()

    if password == "":
        print("passhash: leeres Passwort", file = sys.stderr)
        return 1

    salt = secrets.token_bytes(SALT_LEN)
    key = hashlib.pbkdf2_hmac("sha256", password.encode("utf-8"), salt, ITERATIONS, KEY_LEN)

    print(f"{PREFIX}${ITERATIONS}${b64(salt)}${b64(key)}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
