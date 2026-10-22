#!/usr/bin/env bash
# Legt einen Benutzer für die goteach-Analyse auf Fly.io an oder setzt sein
# Passwort neu. Alle anderen Einträge in AUTH_USERS bleiben erhalten.
#
# Fly zeigt den Wert eines Secrets nicht an. Das Skript liest ihn deshalb aus
# der laufenden Maschine (fly ssh console), prüft jeden Eintrag, ersetzt oder
# ergänzt genau einen und schreibt den Wert zurück. Passt etwas nicht, bricht
# es ab, ohne etwas zu ändern.
#
#     scripts/fly-auth-user.sh neo
#
# Voraussetzungen: flyctl (angemeldet, Maschine läuft) und Go >= 1.22 oder
# python3 für den Hash.
# Das Passwort wird ohne Echo gelesen. Es landet weder in der Shell-History
# noch in einem Programmargument; an Fly geht nur der Hash.
# `fly secrets set` startet die Maschinen neu; laufende Analysen brechen ab.
#
# Bewusst bash-3.2-tauglich (macOS): keine assoziativen Arrays.

set -euo pipefail

APP="${FLY_APP:-goteach-prod}"
NAME="${1:-}"

# Strenger als internal/auth/users.go (dort nur: kein ":" und kein ","),
# damit der Name auch in Logs und Shells harmlos bleibt.
if [[ ! "${NAME}" =~ ^[A-Za-z0-9._-]{1,64}$ ]]; then
    echo "Aufruf: $0 <name>   (Buchstaben, Ziffern, Punkt, Unterstrich, Bindestrich)" >&2
    exit 2
fi

FLY="$(command -v fly || command -v flyctl || true)"

if [[ -z "${FLY}" ]]; then
    echo "flyctl fehlt (https://fly.io/docs/flyctl/install/)." >&2
    exit 1
fi

# Hash mit Go (cmd/passhash) oder, ohne Go, mit Python (scripts/passhash.py).
# Beide erzeugen dasselbe Format.
if command -v go > /dev/null; then
    HASHER="go"
elif command -v python3 > /dev/null; then
    HASHER="python3"
else
    echo "Weder Go noch python3 gefunden; eins davon wird für den Hash gebraucht." >&2
    exit 1
fi

# Beide Hash-Werkzeuge liegen relativ zum Repository.
cd "$(dirname "$0")/.."

trim() {
    local s="$1"

    s="${s#"${s%%[![:space:]]*}"}"
    s="${s%"${s##*[![:space:]]}"}"
    printf '%s' "${s}"
}

# --- 1. Laufenden Wert lesen ---------------------------------------------

echo "Lese AUTH_USERS aus der laufenden Maschine von ${APP} ..." >&2

if ! raw="$("${FLY}" ssh console -a "${APP}" -C 'printenv AUTH_USERS')"; then
    echo "fly ssh console ist fehlgeschlagen. Läuft eine Maschine?" >&2
    echo "Starten etwa mit: ${FLY} machine start -a ${APP}" >&2
    exit 1
fi

# flyctl kann vor dem Wert Hinweise ausgeben. Der Wert ist die letzte Zeile
# mit einem Hash; jeder Eintrag darin wird unten noch einzeln geprüft.
current="$(printf '%s\n' "${raw}" | tr -d '\r' | grep 'pbkdf2-sha256[$]' | tail -n 1 || true)"

if [[ -z "${current}" ]]; then
    echo "AUTH_USERS ist in der Maschine leer oder nicht lesbar. Nichts geändert." >&2
    echo "Für die Ersteinrichtung siehe README, Abschnitt Authentifizierung." >&2
    exit 1
fi

# --- 2. Einträge prüfen, den eigenen herausnehmen ------------------------

hash_re='^pbkdf2-sha256[$][^[:space:]]+$'
kept=()
replaced=0

IFS=',' read -r -a entries <<< "${current}"

for entry in "${entries[@]}"; do
    entry="$(trim "${entry}")"

    if [[ -z "${entry}" ]]; then
        continue
    fi

    name="$(trim "${entry%%:*}")"
    hash="$(trim "${entry#*:}")"

    if [[ "${entry}" != *:* || -z "${name}" || ! "${hash}" =~ ${hash_re} ]]; then
        echo "Unerwarteter Eintrag in AUTH_USERS (Name: '${name}'). Nichts geändert." >&2
        exit 1
    fi

    if [[ "${name}" == "${NAME}" ]]; then
        replaced=1
        continue
    fi

    kept+=("${name}:${hash}")
done

# --- 3. Neues Passwort lesen und hashen ----------------------------------

read -r -s -p "Neues Passwort für ${NAME}: " pass1
echo >&2
read -r -s -p "Passwort wiederholen: " pass2
echo >&2

if [[ "${pass1}" != "${pass2}" ]]; then
    echo "Die Passwörter stimmen nicht überein. Nichts geändert." >&2
    exit 1
fi

if (( ${#pass1} < 12 )); then
    echo "Bitte mindestens 12 Zeichen. Nichts geändert." >&2
    exit 1
fi

if [[ "${HASHER}" == "go" ]]; then
    new_hash="$(printf '%s' "${pass1}" | go run ./cmd/passhash)"
else
    new_hash="$(printf '%s\n' "${pass1}" | python3 scripts/passhash.py)"
fi

unset pass1 pass2

if [[ ! "${new_hash}" =~ ${hash_re} ]]; then
    echo "Das Hash-Werkzeug lieferte keinen gültigen Hash. Nichts geändert." >&2
    exit 1
fi

kept+=("${NAME}:${new_hash}")

# --- 4. Bestätigen und schreiben -----------------------------------------

names=""

for entry in "${kept[@]}"; do
    names="${names:+${names}, }${entry%%:*}"
done

if (( replaced == 1 )); then
    echo "${NAME}: Passwort wird neu gesetzt." >&2
else
    echo "${NAME}: wird neu angelegt." >&2
fi

echo "Benutzer danach: ${names}" >&2
read -r -p "AUTH_USERS auf ${APP} jetzt setzen? Die Maschinen starten neu. [j/N] " answer

if [[ "${answer}" != "j" && "${answer}" != "J" ]]; then
    echo "Abgebrochen. Nichts geändert." >&2
    exit 0
fi

joined="$(IFS=','; printf '%s' "${kept[*]}")"
"${FLY}" secrets set -a "${APP}" "AUTH_USERS=${joined}"

echo "Fertig. Anmelden mit ${NAME} auf https://flaleer-berlin.vercel.app/analyse" >&2
