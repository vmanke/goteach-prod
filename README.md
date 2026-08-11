# goteach — Go-Analyse mit Teaching pro Zug

Umsetzung der Go-Analysearchitektur: symbolischer Brettzustand →
Gruppensegmentierung → KataGo-Ownership → situatives Stärkemaß. Eingabe ist
immer eine SGF-Partie; es gibt keinen Bildpfad, weil Go-Partien als SGF
vorliegen und KataGo darauf rechnet. Kernfeature bleibt **Teaching pro Zug**:
Für jeden Zug einer SGF-Partie entsteht eine deutsche Lehreinheit mit
Bewertungskategorie, Punktverlust, Gewinnchancen-Verlauf, Engine-Erstwahl
samt Variante, Gruppeneffekten (Stärke, Atari, Schlagen, Benson-Status)
und einem faktenbasierten Merksatz.

## Pakete

| Paket              | Inhalt                                                                 |
|--------------------|------------------------------------------------------------------------|
| `board`            | Brett, Züge, Schlagen, Selbstmord-/Ko-Verbot, Zobrist-Hash, SGF, Koordinaten |
| `groups`           | Ketten mit Freiheiten; Bensons unbedingtes Leben (1976) als exakter Cross-Check |
| `strength`         | Situative Gruppenstärke je Kette und als Feld über alle Punkte (exp(−d/τ)) |
| `katago`           | Client der KataGo Analysis Engine (JSON/stdin-stdout) + Mock für Tests + Remote-Client (`/engine/analyze`) |
| `teaching`         | Teaching pro Zug: Reports, deutscher Lehrtext, optionaler LLM-Feinschliff |
| `shapes`           | Benannte Formen: Schablonen mit Symmetrien plus Leiter, Netz, Schnapp  |
| `internal/auth`    | PBKDF2-HMAC-SHA256 und JWT HS256 (stdlib-only)                         |
| `internal/dotenv`  | Minimaler .env-Loader (Secrets nie in Flags oder Logs)                 |
| `internal/server`  | HTTP-Dienst + Web-Frontend (Upload, Download, SEO, Hydration, JWT-Login, Engine-Passthrough) |
| `cmd/goteach`      | CLI                                                                     |
| `cmd/passhash`     | Passwort-Hashes für `AUTH_USERS` erzeugen                              |

## Build & Tests

Voraussetzung: Go ≥ 1.22.

```bash
go build -o goteach ./cmd/goteach
go test ./...
```

## KataGo-Setup

1. KataGo-Binary und ein Netz (`.bin.gz`) von
   https://github.com/lightvector/KataGo beziehen.
2. Die mitgelieferte `analysis.cfg` verwenden oder eigene Config anpassen.

**Verbindliche Annahme:** `reportAnalysisWinratesAs = BLACK`.
Laut offizieller `Analysis_Engine.md` gelten *alle* Werte (Winrate,
ScoreLead, Ownership) in der Perspektive dieses Config-Schlüssels; das
offizielle `analysis_example.cfg` setzt BLACK. goteach normiert intern von
Schwarz-Sicht auf die Sicht des jeweils Ziehenden. Ownership wird laut Doku
row-major ab oben links (A19) bis unten rechts (T1) geliefert — exakt die
interne `board.Idx`-Ordnung; die Länge wird zur Laufzeit validiert.

## Nutzung

Echte Analyse:

```bash
./goteach -sgf partie.sgf \
          -katago /pfad/zu/katago \
          -model  /pfad/zu/kata-netz.bin.gz \
          -config analysis.cfg \
          -visits 200 -json report.json
```

Offline-Demo ohne KataGo (SYNTHETISCHE Werte, deutlich gebannert):

```bash
./goteach -sgf demo/demo.sgf -mock -moves
```

Standardansicht sind die **Erzählstränge**; `-moves` schaltet zusätzlich die
Lehreinheit je Zug frei. Der Mock findet auf der Demo-Partie keine Stränge —
ohne `-moves` bleibt die Ausgabe deshalb bis auf die Zusammenfassung leer.

Wichtige Flags: `-moves` (Lehreinheit je Zug), `-from/-to` (Zugbereich),
`-tau` (Abklinglänge des Stärkemaßes, Default 3.0), `-rules`, `-komi`,
`-json`.

Beispielausgabe (aus der **Mock**-Demo; Zahlen daher synthetisch):

```
Keine Erzählstränge gefunden — die Partie verlief ohne erkennbar zusammenhängende Kämpfe.

Zug 1 — Schwarz Q16 [ausgezeichnet, +14.2 Pkt]. Gewinnchance Schwarz: 50.0 % → 77.5 %.
Engine-Erstwahl: A19.
Schwarze Kette um Q16 (1 Stein(e), 4 Freiheit(en)): Stärke 0.00 → 0.38.
Merksatz: Solide. Beobachten Sie, wie sich die Ownership in der Umgebung des Zuges verschiebt.

Zusammenfassung
---------------
Schwarz: ausgezeichnet ×5 | Ø Punktverlust -9.82
Weiß: ausgezeichnet ×5 | Ø Punktverlust -10.26
```

## HTTP-Dienst (`internal/server`)

Derselbe Analyse-Kern als HTTP-Dienst mit Web-Frontend. Die Logik liegt
in `internal/server`; `main.go` (Repo-Wurzel) und `cmd/server/main.go`
sind identische Wrapper.

```bash
go build -o goteach-server .
PORT=8080 ./goteach-server
```

| Endpunkt        | Funktion                                                    |
|-----------------|-------------------------------------------------------------|
| `GET /`         | Startseite: HTML für Browser (Accept: text/html), sonst Dienstinfo als JSON |
| `GET /api`      | Dienstinfo (JSON)                                           |
| `GET /healthz`  | Liveness-Check                                              |
| `POST /login`   | Zugangsdaten → JWT (nur bei aktiver Auth, sonst 404)        |
| `POST /analyze` | SGF → Teaching-Reports als JSON (bei aktiver Auth: `Authorization: Bearer <Token>`) |
| `POST /engine/analyze` | Engine-Passthrough für Remote-Instanzen (nur mit `KATAGO_ENGINE_TOKEN`, sonst 404) |
| `GET /robots.txt`, `GET /sitemap.xml` | SEO                                   |
| `GET /app.js`, `GET /style.css`, `GET /favicon.svg` | eingebettete Assets     |

`POST /analyze` akzeptiert die Partie auf vier Wegen:

- **roher Body** (wie bisher): `curl --data-binary @partie.sgf …/analyze`
- **Datei-Upload** (multipart, Feld `sgf`): `curl -F "sgf=@partie.sgf" …/analyze`
- **Formularfeld** `sgf` (URL-kodiert) — so funktioniert das Formular der
  Startseite auch ohne JavaScript
- **OGS-Import** (Parameter `ogs`, URL oder Partie-ID): der Server lädt das
  SGF öffentlicher Partien von online-go.com
  (`curl -X POST "…/analyze?ogs=https://online-go.com/game/12345678"`);
  ein mitgeliefertes SGF hat Vorrang

Query-Parameter: `visits` (Default 50, Deckel 1000), `tau`, `from`, `to`,
`rules`, `komi`; bei Formular-/Multipart-Posts auch als Formularfelder
(Query gewinnt). `download=1` liefert den Report zusätzlich mit
`Content-Disposition: attachment` als Datei-Download.

Das Frontend ist serverseitig gerendert (SEO: Canonical, Open Graph,
JSON-LD, robots.txt, sitemap.xml) und hydratisiert sich über
`assets/app.js`: eingebetteter Zustand in `#goteach-state`, Drag & Drop,
Ergebnis-Rendering, clientseitiger JSON-Download, Hell-/Dunkel-Umschalter
(folgt sonst `prefers-color-scheme`). Ohne JavaScript bleibt das Formular
als klassischer Multipart-POST nutzbar.

Engine-Wahl über Umgebung: Sind `KATAGO_PATH` und `KATAGO_MODEL` gesetzt
(optional `KATAGO_CONFIG`, Default `analysis.cfg`), startet pro Anfrage
eine echte Engine. Ohne lokale Binary delegiert `KATAGO_REMOTE_URL` die
Analyse an einen entfernten Engine-Host (siehe „Remote-Engine“).
Andernfalls antwortet der Mock — die Antwort trägt dann
`"synthetic": true` und die Werte sind **keine echte Analyse**.

### Authentifizierung (JWT)

Standardmäßig läuft der Dienst offen. Sobald `AUTH_USERS` gesetzt ist,
verlangt `POST /analyze` ein JWT; alle anderen Routen (Startseite,
Dienstinfo, Healthcheck, Assets) bleiben öffentlich. Es gibt bewusst
keine Registrierung, keinen Passwort-Reset und keine Datenbank — die
Benutzer stehen als `name:hash`-Paare in der Umgebung, komplett
stdlib-implementiert (PBKDF2-HMAC-SHA256 + JWT HS256).

| Variable          | Bedeutung                                              |
|-------------------|--------------------------------------------------------|
| `AUTH_USERS`      | `alice:pbkdf2-sha256$…,bob:pbkdf2-sha256$…` (kommagetrennt) |
| `AUTH_JWT_SECRET` | HMAC-Secret der Tokens; **Pflicht** sobald `AUTH_USERS` gesetzt ist (sonst startet der Server nicht) |
| `AUTH_TOKEN_TTL`  | Token-Lebensdauer, `time.ParseDuration`-Syntax (Default `24h`) |
| `GOTEACH_REQUIRE_AUTH` | Verbietet offenen Betrieb: fehlt `AUTH_USERS`, startet der Server nicht. `0`/`false` schaltet ab |

**Empfehlung für öffentlich erreichbare Instanzen:** `GOTEACH_REQUIRE_AUTH=1`
setzen. Ohne `AUTH_USERS` ist `POST /analyze` sonst für jeden offen — und
jede Anfrage bindet die Maschine minutenlang mit KataGo. Ein vergessenes
oder vertipptes Secret fällt sonst nur als Log-Zeile auf. Bewusst ein
ausdrücklicher Schalter statt einer automatischen Umgebungserkennung: Der
Dienst soll nicht raten, ob er „in Produktion" läuft, und ein Fehlschluss
soll nicht das nächste Deployment beim Start umbringen.

Hash erzeugen und einloggen:

```bash
echo -n 'geheim' | go run ./cmd/passhash            # → pbkdf2-sha256$600000$…$…
export AUTH_USERS="alice:$(echo -n 'geheim' | go run ./cmd/passhash)"
export AUTH_JWT_SECRET="$(head -c 32 /dev/urandom | base64)"

TOKEN=$(curl -s -X POST localhost:8080/login \
  -d '{"username":"alice","password":"geheim"}' | sed -n 's/.*"token": "\([^"]*\)".*/\1/p')
curl -X POST --data-binary @partie.sgf \
  -H "Authorization: Bearer $TOKEN" localhost:8080/analyze
```

Details: `POST /login` nimmt JSON `{"username":…,"password":…}` (max.
4 KiB) und antwortet mit `{"token":…,"tokenType":"Bearer","expiresAt":…}`.
Falscher Name und falsches Passwort ergeben dieselbe 401-Antwort (kein
User-Enumeration; unbekannte Namen kosten per Dummy-Hash gleich viel
Zeit). Tokens sind HS256-signiert (`alg` gepinnt, `none` abgelehnt),
laufen nach `AUTH_TOKEN_TTL` ab (30 s Toleranz für Uhrenversatz) und
werden in konstanter Zeit verglichen. Zusätzlich zur Signatur prüft
jede Anfrage, ob der Benutzer aus dem Token noch in `AUTH_USERS` steht —
einen Eintrag entfernen sperrt also sofort aus, nicht erst beim
Token-Ablauf (Passwort-Änderung bei gleichem Namen lässt bestehende
Tokens dagegen bis `exp` gültig; wer das braucht, rotiert
`AUTH_JWT_SECRET` und loggt damit alle aus). Die Startseite zeigt bei aktiver
Auth ein Login-Formular; das Token lebt nur im `sessionStorage` des
Tabs. Ein Rate-Limit auf `/login` gibt es bewusst nicht (der Dienst ist
zustandslos); die PBKDF2-Kosten bremsen Brute-Force serverseitig.
Hinweis: Das No-JS-Formular funktioniert bei aktiver Auth nicht — der
Multipart-POST kann keinen Bearer-Header setzen und erhält 401.

### Remote-Engine: echte Analysen auf Vercel

Vercel kann kein KataGo-Binary ausführen (Größen-/Zeitlimits, keine
GPU) — dort lief bisher nur der Mock. Die Lösung ist Arbeitsteilung
über zwei Instanzen desselben Servers:

1. **Engine-Host** (Docker-Image auf VPS, Fly.io, Railway …): hat die
   echte Engine und schaltet mit `KATAGO_ENGINE_TOKEN` den Passthrough
   `POST /engine/analyze` frei. Ohne die Variable existiert die Route
   nicht (404). Der Endpunkt verlangt das Token als `Authorization:
   Bearer` (Vergleich in konstanter Zeit), deckelt `maxVisits` erneut
   und nutzt ausschließlich die lokale Engine — nie selbst eine
   Remote-Engine, damit sich zwei Instanzen nicht endlos gegenseitig
   weiterreichen.
2. **Vercel-Instanz**: `KATAGO_REMOTE_URL` (Basis-URL des Hosts) und
   `KATAGO_REMOTE_TOKEN` (gleicher Wert wie `KATAGO_ENGINE_TOKEN`)
   setzen — weiterhin **keine** `KATAGO_PATH`/`KATAGO_MODEL`. Jede
   Analyse läuft dann über den Host; `"synthetic"` in der Antwort
   meldet, ob dort wirklich eine Engine gerechnet hat (antwortet der
   Host selbst mit dem Mock, bleibt das Flag `true`). Fehler des Hosts
   erscheinen als `502 Bad Gateway`.

Wire-Format des Passthrough (JSON): Request
`{"request":{"initialStones":…,"moves":…,"rules":…,"komi":…,"size":…,"maxVisits":…},"turns":[…]}`,
Antwort `{"synthetic":…,"results":[…]}` mit den rohen
KataGo-Ergebnissen pro Turn. Der Remote-Client (`katago.Remote`) hat
5 Minuten Timeout pro Analyse; auf Vercel begrenzt zusätzlich das
Funktions-Zeitlimit die Antwortzeit — `visits` dort moderat wählen
(Analysen skalieren mit Visits × Stellungen).

### Docker: echte KataGo-Engine

Vercel-Functions haben keine GPU und taugen nicht als Engine-Host — dort
läuft der Mock. Für echte Analysen bündelt das `Dockerfile` alles in
einen Container: Go-Server, KataGo (CPU/Eigen, AVX2) und ein starkes,
kleines Transformer-Netz (`b10c384h6nbttflrs`, 36 MB) aus dem offiziellen
KataGo-Release v1.17.1. Beide Downloads werden im Build gegen gepinnte
SHA256-Summen verifiziert (bei anderen `KATAGO_*`-Build-Args die
passenden Summen mitliefern).

```bash
docker build -t goteach .
docker run -p 8080:8080 goteach          # oder: docker compose up --build
curl --data-binary @partie.sgf "http://localhost:8080/analyze?visits=50"
```

**Zugriff auf KataGo:** direkt nie — der Server startet KataGo pro
`/analyze`-Anfrage als Kindprozess (Analysis Engine, JSON über
stdin/stdout). Die Umgebungsvariablen sind im Image vorbelegt
(`KATAGO_PATH=/app/katago/AppRun`, `KATAGO_MODEL=/app/net.bin.gz`,
`KATAGO_CONFIG=/app/analysis.cfg`) und lassen sich per `-e`/Volume
überschreiben, z. B. für ein anderes Netz. Nach außen bleibt alles die
gewohnte HTTP-API; Antworten tragen dann `"synthetic": false`.

Hinweise:

- **CPU-Kosten:** Der Engine-Start pro Anfrage lädt das Netz (≈ 10–20 s
  auf CPU); Analysen skalieren mit Visits × Stellungen. Mit `visits`,
  `from`/`to` dosieren.
- **Thread-Tuning:** Die `analysis.cfg` ist bewusst konservativ
  (`numAnalysisThreads = 1`, `numSearchThreadsPerAnalysisThread = 4`,
  `nnMaxBatchSize = 8`), damit kleine Cloud-Container (Railway, Render …)
  stabil bleiben: Sättigt KataGo alle vCPUs, verhungert der Healthcheck
  und der Orchestrator stoppt den Container mitten in der Analyse
  („Stopping Container“). Auf dicker Hardware ohne Rebuild hochdrehen —
  Faustregel Suchthreads ≈ vCPUs − 1:
  `KATAGO_OVERRIDES="numSearchThreadsPerAnalysisThread=16,nnMaxBatchSize=32"`
  (Format `schlüssel=wert,…`, wird als `-override-config` an KataGo
  durchgereicht; im CLI als Flag `-overrides`).
- **CPU ohne AVX2:** mit `--build-arg KATAGO_FLAVOR=eigen` bauen. Die
  Release-Binaries sind x64; auf ARM (z. B. Apple Silicon) KataGo selbst
  kompilieren oder auf einem x64-Host deployen.
- **Arbeitsteilung:** der Docker-Host (VPS, Fly.io, Railway …) liefert
  die echte Analyse; Vercel nutzt ihn per `KATAGO_REMOTE_URL` als
  Remote-Engine (siehe „Remote-Engine“) oder bleibt ohne sie Demo (Mock).

### Fly.io-Deployment (echte Engine)

Die committete `fly.toml` deployt das Dockerfile mit sinnvollen
Voreinstellungen — 4 Performance-Kerne (KataGo sättigt alle), Warteschlange
statt paralleler Analysen, Healthcheck auf `/healthz`, Schlafen ohne
Verkehr:

```bash
fly launch --no-deploy   # einmalig; übernimmt fly.toml (App-Name anpassen)
fly deploy
curl -s https://<app>.fly.dev/api   # "katago": true
```

Danach in der Vereinsseite `content::ANALYZE_URL` auf
`https://<app>.fly.dev/analyze` stellen (der Deploy-Test dort erinnert an
die zugehörige CSP-Zeile). Der Fly-Proxy trennt still stehende
Verbindungen; `/analyze` hält sie deshalb mit Heartbeat-Füllbytes offen —
lange Partien dauern trotzdem lange, moderate `visits` oder
`from`/`to`-Abschnitte bleiben sinnvoll.

**Automatisch statt von Hand:** `.github/workflows/fly-deploy.yml` deployt
bei jedem Push auf `main` (und per Knopfdruck unter Actions → Fly Deploy).
Einmalig einzurichten: in der Fly-Weboberfläche unter der App → **Tokens**
ein Deploy-Token erzeugen und es im GitHub-Repo unter Settings → Secrets
and variables → Actions als **`FLY_API_TOKEN`** hinterlegen. Ohne das
Secret schlägt der Workflow mit einer Auth-Meldung fehl und richtet nichts
an.

### Vercel-Deployment

Vercels Go-Support baut den Server nur im **Standalone-Modus**, wenn das
Framework-Preset `go` ist; andernfalls greift der alte
Serverless-Function-Builder, baut das falsche Ziel und jede Anfrage endet
mit `FUNCTION_INVOCATION_FAILED`. Die `vercel.json` im Repo pinnt deshalb
beides — das überstimmt die Projekteinstellungen bei jedem Deployment:

- `"framework": "go"` erzwingt den Standalone-Modus;
- `"buildCommand": "go build -o \"$VERCEL_OUTPUT_FILE\" ."` baut garantiert
  den Server aus der Repo-Wurzel. Wichtig, weil auch der Standalone-Modus
  einen Build-Command-Override aus den Projekteinstellungen ehrt — steht
  dort z. B. die CLI-Buildzeile aus diesem README, deployt Vercel die CLI,
  die sofort mit der Flag-Usage endet (genau dieses Fehlerbild gab es).

Im Vercel-Dashboard sollten Install-/Build-Command-Overrides trotzdem
**aus** sein und niemals `KATAGO_PATH`/`KATAGO_MODEL` gesetzt werden
(Functions haben keine Engine-Binary). Für echte Analysen stattdessen
`KATAGO_REMOTE_URL` und `KATAGO_REMOTE_TOKEN` auf den Engine-Host
zeigen lassen (siehe „Remote-Engine“); ohne beides antwortet der Mock.
Für den JWT-Schutz `AUTH_USERS` und `AUTH_JWT_SECRET` als
Environment-Variablen eintragen. Der Server lauscht auf dem `PORT` aus
der Umgebung; das Root Directory des Projekts muss auf die Repo-Wurzel
zeigen.

## LLM-Feinschliff (optional)

```bash
echo 'ANTHROPIC_API_KEY=sk-ant-...' > .env   # NIE committen (.gitignore!)
./goteach -sgf partie.sgf ... -llm
```

Standardmodell ist **`claude-fable-5`**, überschreibbar per `-llm-model`.
Gültige aktuelle Modell-IDs (Messages API): `claude-fable-5`,
`claude-opus-5`, `claude-sonnet-5`, `claude-opus-4-8`, `claude-sonnet-4-6`,
`claude-haiku-4-5`.

Hinweise zu `claude-fable-5`:

* Preis $10 Input / $50 Output pro Million Token — bei langen Partien
  `-from/-to` nutzen oder ein günstigeres Modell wählen.
* Erfordert 30-Tage-Datenaufbewahrung beim API-Konto; Organisationen mit
  Zero Data Retention erhalten pauschal HTTP 400 — dann z. B.
  `-llm-model claude-opus-5` verwenden.
* Sicherheits-Klassifizierer können einzelne Anfragen ablehnen
  (`stop_reason: refusal`); goteach aktiviert dafür den serverseitigen
  Fallback (`fallbacks: "default"`), sodass ein Ersatzmodell antwortet.
  Schlägt der Feinschliff dennoch fehl, bleibt der verifizierte Basistext
  erhalten.

Designprinzip aus dem Architekturbericht: Das LLM rechnet nicht. Es erhält
ausschließlich die bereits verifizierten Zahlen/Züge als JSON und darf nur
umformulieren; der verifizierte Basistext bleibt immer erhalten
(`text` vs. `textLLM` im JSON-Export).

**Kostenhinweise** (werden auch zur Laufzeit auf stderr gemeldet):
KataGo-Rechenaufwand ≈ Visits × (Zuganzahl + 1); `-llm` erzeugt einen
API-Call pro Zug — bei langen Partien `-from/-to` nutzen.

## Erzählstränge statt Zugliste

Eine Partie mit 250 Zügen ergab bisher 250 Lehreinheiten, die nichts
voneinander wussten — eine Textwand. `goteach` zerlegt eine Partie deshalb
zuerst in **Erzählstränge**: zusammenhängende Abschnitte über Brettgegend und
Zugbereich, mit benannten Formen und nachgerechneter Bilanz.

```bash
./goteach -sgf partie.sgf -mock                  # Stränge (Standard)
./goteach -sgf partie.sgf -mock -moves           # zusätzlich jeder Zug einzeln
./goteach -sgf partie.sgf ... -refine-visits 800 # Rechenzeit in die Entscheidung
```

Beispielausgabe:

```
[3] Oben links, Züge 12 bis 48 (19 davon gehören hierher). Beteiligte Formen:
    Leiter D17, Kreuzschnitt E16, leeres Dreieck C15. Schwarz verliert hier
    11.4 Punkte. 6 Steine werden geschlagen. Der teuerste Zug ist 34
    (Schwarz D18, Fehler, 5.2 Punkte).
    gekoppelt: Leiter D17 ↔ Kreuzschnitt E16 (r = +0.81, Versatz 3)
```

### Wie ein Strang entsteht

1. **Salienztensor.** Aus den Ownership-Feldern, die KataGo ohnehin je
   Stellung liefert, entsteht `S[t, y, x]` — wie stark sich die Zugehörigkeit
   jedes Punktes mit jedem Zug ändert. Diese Felder wurden bisher nach dem
   Ableiten der Skalare verworfen; sie aufzubewahren kostet rund 0,7 MB je
   Partie und ist die Voraussetzung für alles Weitere.
2. **Formen benennen** (Paket `shapes`). Deterministisch, ohne Modell: leeres
   Dreieck, Bambusverbindung, Tigermaul, Kosumi, Ein- und Zwei-Punkte-Sprung,
   Kleiner und Großer Springerzug, Kreuzschnitt — jeweils über alle acht
   Symmetrien des Quadrats und beide Farbrollen. Dazu die Motive, die eine
   Variantensuche brauchen: **Leiter**, **Netz** und **Schnapp**. Damit löst
   das Paket ein Versprechen ein, das der Lehrtext bisher an den Spieler
   weiterreichte („lesen Sie die Leiter") — ohne es selbst zu können.
3. **Formen in Relation setzen.** Jede Forminstanz bekommt zwei Zeitspuren:
   ihre Salienz und ihre Stärke (aus `strength.Field`, dem Stärkemaß pro
   Punkt statt pro Kette). Zwischen je zwei Formen wird die normierte
   Kreuzkorrelation mit Versatz gerechnet.
4. **Fenstern.** Stränge sind die Zusammenhangskomponenten dieses
   Kopplungsgraphen — nicht bloß räumlich benachbarte Gebiete. Damit landet
   die Leiter am einen Brettrand im selben Strang wie der Kampf am anderen,
   den sie entscheidet.
5. **Zuordnen und rechnen.** Jeder Zug geht an höchstens einen Strang, den
   räumlich nächsten; nur so ist die Bilanz eines Strangs nachrechenbar. Ein
   Zug weitab jeder erkannten Form gehört zu keinem Strang — das ist der
   Normalfall für ruhige Passagen und keine Lücke.

### Statistische Absicherung

Bei einem Dutzend Formen werden 66 Paare geprüft, jeweils über 41 Versätze.
Ohne Absicherung wäre der Kopplungsgraph zum großen Teil Einbildung. Drei
Vorkehrungen:

- **Permutationstest je Paar.** Die Nullverteilung entsteht durch zyklisches
  Verschieben einer Spur — das zerstört den Zusammenhang, erhält aber Länge
  und Eigenkorrelation. Die Verschiebung muss dabei den Versatzbereich
  überschreiten, sonst findet die Versatzsuche denselben Zusammenhang einfach
  wieder und die „Nullverteilung" enthält noch das Signal, das sie widerlegen
  soll.
- **Fehltrefferkontrolle** über Benjamini-Hochberg. Die Korrektur läuft über
  *alle* geprüften Paare; erst danach greift die Effektstärke-Schranke.
  Umgekehrt wäre sie schwächer statt strenger, weil große Korrelationen
  kleine p-Werte haben.
- **Effektstärke-Schranke.** Existiert eine Form nur kurz, sind alle ihre
  Korrelationen winzig — und dann liegt schon ein r von 0.01 weit über der
  mitgeschrumpften Nullverteilung. Signifikanz ohne Effektstärke ist keine
  Erkenntnis, deshalb gilt zusätzlich |r| ≥ 0.30.

**Und eine Sprachregel:** Korrelation ist keine Kausalität. Der Lehrtext sagt
„hängt zeitlich zusammen mit", nie „wurde entschieden durch" — im
Strang-Prompt (`teaching/llm.go`) steht das als harte Regel.

### Gelerntes Salienzmodul (optional)

Die Fensterung lässt sich einem gelernten Modul überlassen — dem Teilprojekt
`analysis/python/` (Paket `goteach-salience`). Es bekommt den Partieverlauf
als JSON und liefert Fenster zurück; Formen und Zahlen kommen weiterhin aus
demselben deterministischen Go-Code. **Das Modul wählt aus, es behauptet
nichts.**

```bash
cd analysis/python && pip install -e ".[dev,onnx,train]"
export GOTEACH_SALIENCE_CMD="python3 -m goteach_salience score -"
./goteach -sgf partie.sgf -mock
```

Ohne gesetzte Variable — und ebenso, wenn der Aufruf scheitert — übernimmt
die deterministische Fensterung. Das Modul ist eine Verbesserung, keine
Voraussetzung.

Das Netz ist ein Encoder-Decoder mit **bidirektionaler Rückkopplung**: An
jeder Nichtlinearität trifft ein Signal von unten auf eines von oben, und
welches ankommt, wird stochastisch gezogen. Zur Laufzeit wird über beide
Richtungen gemittelt statt gewürfelt — sonst ergäbe dieselbe Partie zweimal
analysiert nicht dasselbe Ergebnis, und der Export prüft genau das nach.
Gelernt wird selbstüberwacht: vorhergesagt wird, wie stark sich die
Zugehörigkeit eines Punktes in den nächsten zwölf Zügen noch verändert.
Details in `analysis/python/README.md`.

**Stand:** Die deterministische Fensterung liefert derzeit die besseren
Stränge. Das Modul ist gebaut, geprüft und angebunden, aber auf synthetischen
Partien trainiert; für belastbare Fenster bräuchte es echte Partien mit
echter KataGo-Ownership.

### Rückkopplung: Rechenzeit dorthin, wo es zählt

Mit `-refine-visits` rechnet KataGo in einer zweiten Runde die Stellungen der
stärksten Stränge mit höherer Visit-Zahl nach. Die Segmentierung bleibt dabei
unangetastet — sie war die Grundlage der Auswahl; sie nachträglich mit den
verfeinerten Zahlen zu verschieben hieße, sich im Kreis zu drehen.

### Kosten

Der LLM-Feinschliff kostet einen Aufruf **je Strang** statt je Zug. Eine
Partie mit 250 Zügen kommt damit auf eine Handvoll Anfragen statt auf 250.
Mit `-moves` lässt sich der Feinschliff pro Zug weiterhin dazuschalten.

### Grenzen

- Der `-mock`-Analyzer benutzt die Distanz zum *nächstgelegenen* Stein. Auf
  dicht besetzten Brettern sättigt dieses Feld, die Salienzspuren bestehen
  dann aus wenigen Ausschlägen und es entstehen keine Stränge. Das ist eine
  Eigenschaft des Mocks, nicht der Analyse: KataGos Ownership verschiebt sich
  mit jedem Zug über das ganze Brett.
- Fenstergrenzen sind eine Erzählentscheidung, keine Spielwahrheit. Die
  Fakten je Strang werden deshalb auf dem ganzen Brett gerechnet.
- Der Formenkatalog ist nicht vollständig. Ein unbenanntes Motiv erscheint
  nicht als „nichts Besonderes", sondern gar nicht — der Strang wird dann
  ohne es erzählt. **Josekis fehlen bewusst**: Das sind Zugfolgen, keine
  lokalen Muster, und sie bräuchten eine Sequenzdatenbank.

## Grenzen (bewusst und dokumentiert)

- Einfaches Ko statt Positional Superko (für legale SGF-Partien ausreichend;
  KataGo prüft serverseitig ohnehin regelkonform).
- Benson ist beweisbar korrekt, aber konservativ (Seki zählt nicht als
  unbedingt lebendig).
- Der Mock ist ein Distanz-Abkling-Einflussmodell — ausschließlich für
  Tests/CI/Pipeline-Demos, niemals für echtes Teaching.
- SGF: nur Hauptvariante; rechteckige Bretter werden nicht unterstützt.
- Erzählstränge: Der Kopplungsgraph ist eine explorative Gruppierung mit
  kontrollierter Fehltrefferquote, kein Beleg für taktische Zusammenhänge
  (Details im Abschnitt „Erzählstränge").
- Das gelernte Salienzmodul ist auf synthetischen Partien trainiert; seine
  Fenster sind gröber als die des Kopplungsgraphen.
- Bekannte KataGo-Schwächen aus dem Architekturbericht (zyklische
  Adversarial-Gruppen; Leiter-Fehler bei sehr niedrigen Visits) gelten auch
  hier — im Zweifel `-visits` erhöhen.

## Teaching-Logik (deterministisch)

- Punktverlust = Vorzeichen(Zieher) · (ScoreLead_vorher − ScoreLead_nachher),
  beide Werte Schwarz-Perspektive; Gewinnchancen analog auf die Sicht des
  Ziehenden normiert.
- Kategorien: ≤ 0.5 Pkt oder Engine-Erstwahl → „ausgezeichnet"; ≤ 1.5 „gut";
  ≤ 3 „Ungenauigkeit"; ≤ 6 „Fehler"; sonst „grober Fehler".
- Gruppenstärke s ∈ [−1, +1]:
  `s = Σ e^(−d/τ) · sign(Farbe) · ownership_black  /  Σ e^(−d/τ)`
  mit Multi-Source-BFS-Distanz d zur Kette — die neuronal fundierte Variante
  klassischer Einflussmodelle.
- Merksatz-Priorität (rein faktenbasiert): eigenes Atari > Schlagen >
  gegnerisches Atari > Eigenschwächung (Δ ≥ 0.15) > Engine-Match >
  Verlust > 6 Pkt > Verlust > 1.5 Pkt > Standardhinweis.

## Teststand

`go vet ./...` und `go test ./...` grün. Abgedeckt: Schlagen, Selbstmord-
und Ko-Verbot, SGF-Parsing inkl. Nachspielen, GTP-Roundtrip, Benson
(Zwei-Augen lebendig / Ein-Auge nicht), Stärkemaß-Grenzwerte und
τ-Lokalität, Teaching end-to-end (Mock) inkl. Bereichsauswahl `-from/-to`
sowie die Eingabewege des HTTP-Dienstes (roher Body, Formularfeld,
Datei-Upload, OGS). Für Auth und Remote-Engine zusätzlich: PBKDF2 gegen
publizierte Testvektoren, JWT-Roundtrip inkl. Ablauf/Manipulation/
`alg:none`, Login-Flows (Erfolg, einheitliche 401, Fehlkonfiguration
fail-closed), der Engine-Passthrough (Token, Limits, 404 ohne
Konfiguration) und ein Zwei-Instanzen-Test, bei dem eine Instanz die
Analyse per `KATAGO_REMOTE_URL` an die andere delegiert.

Für die Erzählstränge zusätzlich: der Stärketensor gegen das bestehende
Kettenmaß verankert (für eine Einzelquelle müssen beide denselben Wert
liefern), Formenerkennung in allen acht Symmetrien und beiden Farben, ein
Leiterleser mit Gegenprobe am Ausbruchstein, der Permutationstest gegen
unabhängige Spuren *und* gegen eine echte Kopplung, die Eindeutigkeit der
Zug-Zuordnung, die Reproduzierbarkeit ganzer Stränge und die Rückkopplung,
die nur die Stellungen der stärksten Stränge nachrechnet.

Die Python-Seite prüft `pytest` unter `analysis/python/`: Fensterbildung,
Merkmalsaufbau und der JSON-Vertrag laufen ohne Modellgewichte; die
torch-Tests (Netzform, bidirektionale Rückkopplung, Determinismus der
Inferenz, ONNX-Export gegen torch) überspringen sich, wenn torch fehlt.

## Quellen

- KataGo `docs/Analysis_Engine.md` und `cpp/configs/analysis_example.cfg`
  (Feld-, Ordnungs- und Perspektiven-Verträge; abgerufen 2026-08-05).
- D. B. Benson (1976): Life in the Game of Go. *Information Sciences* 10,
  17–29.
- A. L. Zobrist (1969): A model of visual organization for the game of Go;
  B. Bouzy (2003): Mathematical morphology applied to computer Go —
  Vorbilder des distanzgewichteten Einfluss-/Stärkemaßes.
- Y. Benjamini, Y. Hochberg (1995): Controlling the False Discovery Rate.
  *J. R. Statist. Soc. B* 57, 289–300 — Grundlage der Kantenauswahl im
  Kopplungsgraphen.
- J. Theiler et al. (1992): Testing for nonlinearity in time series: the
  method of surrogate data. *Physica D* 58, 77–94 — Vorbild des zyklischen
  Surrogattests.
- Interner Architekturbericht dieses Projekts (KataGo-Ownership →
  LLM-Interpretationsschicht).
