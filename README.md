# goteach — Go-Analyse mit Teaching pro Zug

Umsetzung der hybriden Go-Analysearchitektur: Stufen 3–6 in Go
(symbolischer Brettzustand → Gruppensegmentierung → KataGo-Ownership →
situatives Stärkemaß) und die Stufen 1–2 — Homographie und preinformed
U-Net — als Python/ONNX-Teilprojekt unter `vision/python/`. Kernfeature
bleibt **Teaching pro Zug**:
Für jeden Zug einer SGF-Partie entsteht eine deutsche Lehreinheit mit
Bewertungskategorie, Punktverlust, Gewinnchancen-Verlauf, Engine-Erstwahl
samt Variante, Gruppeneffekten (Stärke, Atari, Schlagen, Benson-Status)
und einem faktenbasierten Merksatz.

## Pakete

| Paket              | Inhalt                                                                 |
|--------------------|------------------------------------------------------------------------|
| `board`            | Brett, Züge, Schlagen, Selbstmord-/Ko-Verbot, Zobrist-Hash, SGF, Koordinaten |
| `groups`           | Ketten mit Freiheiten; Bensons unbedingtes Leben (1976) als exakter Cross-Check |
| `strength`         | Situative Gruppenstärke: distanzgewichtete Ownership-Aggregation (exp(−d/τ)) |
| `katago`           | Client der KataGo Analysis Engine (JSON/stdin-stdout) + Mock für Tests |
| `teaching`         | Teaching pro Zug: Reports, deutscher Lehrtext, optionaler LLM-Feinschliff |
| `vision`           | Bilderkennung (Stufen 1–2): JSON-Stellungsformat + Aufruf des Erkenners |
| `internal/dotenv`  | Minimaler .env-Loader (Secrets nie in Flags oder Logs)                 |
| `internal/server`  | HTTP-Dienst + Web-Frontend (Upload, Download, SEO, Hydration)          |
| `cmd/goteach`      | CLI                                                                     |

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
./goteach -sgf demo/demo.sgf -mock
```

Wichtige Flags: `-from/-to` (Zugbereich), `-tau` (Abklinglänge des
Stärkemaßes, Default 3.0), `-rules`, `-komi`, `-json`.

Beispielausgabe (aus der **Mock**-Demo; Zahlen daher synthetisch):

```
Zug 1 — Schwarz Q16 [ausgezeichnet, +14.2 Pkt]. Gewinnchance Schwarz: 50.0 % → 77.5 %.
Engine-Erstwahl: A19.
Schwarze Kette um Q16 (1 Stein(e), 4 Freiheit(en)): Stärke 0.00 → 0.38.
Merksatz: Solide. Beobachten Sie, wie sich die Ownership in der Umgebung des Zuges verschiebt.

Zusammenfassung
---------------
Schwarz: ausgezeichnet ×5 | Ø Punktverlust -9.82
Weiß:    ausgezeichnet ×5 | Ø Punktverlust -10.26
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
| `POST /analyze` | SGF → Teaching-Reports als JSON                             |
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
eine echte Engine. Andernfalls antwortet der Mock — die Antwort trägt
dann `"synthetic": true` und die Werte sind **keine echte Analyse**.

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
  `from`/`to` dosieren; `numAnalysisThreads`/`numSearchThreadsPerAnalysisThread`
  in `analysis.cfg` an die vCPUs anpassen.
- **CPU ohne AVX2:** mit `--build-arg KATAGO_FLAVOR=eigen` bauen. Die
  Release-Binaries sind x64; auf ARM (z. B. Apple Silicon) KataGo selbst
  kompilieren oder auf einem x64-Host deployen.
- **Arbeitsteilung:** Vercel bleibt Frontend/Demo (Mock), der
  Docker-Host (VPS, Fly.io, Railway …) liefert die echte Analyse.

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
**aus** sein und keine `KATAGO_*`-Variablen gesetzt werden (Functions
haben keine Engine; ohne die Variablen antwortet der Mock). Der Server
lauscht auf dem `PORT` aus der Umgebung; das Root Directory des Projekts
muss auf die Repo-Wurzel zeigen.

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

## Stufen 1–2: Bilderkennung (PNG → Stellung)

Homographie und preinformed U-Net liegen im Python/ONNX-Stack unter
`vision/python/` (Trainings-/CV-Ökosystem); die Go-Seite ruft ihn als
Subprozess auf und bleibt damit weiterhin ohne externe Abhängigkeiten.
Übergeben wird der schmale Vertrag aus `vision/adapter.go`:

```json
{ "size": 19, "komi": 7.5, "rows": ["...................", "...X...O...", "..."] }
```

`vision.FromJSON(...).Game()` liefert daraus eine analysierbare Stellung
(`initialStones`, ohne Zughistorie).

### Einrichtung

```bash
cd vision/python
pip install -e ".[dev]"                 # Basis: Erkennung ohne ML
pip install -e ".[dev,onnx,train]"      # zusätzlich U-Net-Inferenz und Training
export GOTEACH_VISION_CMD="python3 -m goteach_vision detect -"
```

Ohne gesetztes `GOTEACH_VISION_CMD` ist die Bilderkennung schlicht nicht
eingerichtet: Die CLI sagt das, der HTTP-Dienst antwortet mit 501. Genau so
ist der Vercel-Pfad gedacht, wo kein Python-Stack mitläuft.

### Nutzung

```bash
# eigenständig — stdout ist ausschließlich der JSON-Vertrag
python3 -m goteach_vision detect brett.png --size 19 --komi 7.5

# über goteach: Stellungsbericht statt Teaching pro Zug
goteach -image brett.png -size 19 -mock

# über den HTTP-Dienst
curl -F "image=@brett.png" "localhost:8080/analyze?size=19"
```

Ein Bild trägt kein Komi. Ohne `--komi`/`-komi`/`?komi=` analysiert KataGo
mit Komi 0 — für eine belastbare Bewertung sollte der Wert mitgegeben werden.

Eine erkannte Stellung hat keine Zughistorie. Sie bekommt deshalb einen
**Stellungsbericht** (`teaching.AnalyzePosition`): Gewinnchance, ScoreLead,
Engine-Erstwahl samt Variante und pro Kette Stärke, Freiheiten, Atari- und
Benson-Status. Teaching pro Zug gibt es dort nicht — ohne Zug kein Zugfehler.

### Wie die Erkennung arbeitet

**Stufe 1 (Homographie)** ist bewusst klassisches CV statt eines zweiten
Netzes: Die beiden Linienbüschel eines Go-Gitters bestimmen die Homographie
vollständig, und bei achsparallelen Screenshots degeneriert sie *rechnerisch*
zu Skalierung plus Zuschnitt. Screenshots und Fotos teilen sich damit einen
Codepfad, ohne Fallunterscheidung. Die Brettgröße (9/13/19) entsteht nicht
aus gezählten Linien, sondern aus Projektionsprofilen im entzerrten Bild —
auf dicht besetzten Brettern sind einzelne Linien von Steinen verdeckt, das
periodische Muster aller Linien aber nicht.

**Stufe 2 (preinformed U-Net)** bekommt die Geometrie als Zusatzkanäle:
6×512×512, davon RGB plus Gittermaske und die signierten Abstände zur
nächsten Linie. Weil diese Kanäle die Gitterteilung mitführen, bedienen
dieselben Gewichte 9×9, 13×13 und 19×19. Ausgelesen wird pro Schnittpunkt
über eine Kreisscheibe; die Konfidenz ist der Softmax-Abstand zwischen bester
und zweitbester Klasse.

**Trainingsdaten sind vollständig synthetisch.** Ein prozeduraler Renderer
erzeugt beide Domänen — saubere Diagramme und foto-artige Bretter mit
Holzmaserung, Kugelschattierung, Schlagschatten, Perspektive, Unschärfe,
Rauschen und Verdeckungen. Labels fallen exakt an, weil der Renderer Stellung
und Schnittpunktkoordinaten kennt.

```bash
python3 -m goteach_vision train --steps 2000 --out netz.pt --onnx netz.onnx
python3 -m goteach_vision detect brett.png --weights netz.onnx
```

### Zwei Backends

`--backend classical` entscheidet allein über die Helligkeit der
Schnittpunkte und braucht kein Modell; das ist der CI-Pfad und die
Sanity-Baseline. `--backend onnx` nutzt das U-Net (`--weights`). Ohne
`--weights` wählt `auto` den klassischen Pfad.

### Stand und Grenzen (gemessen, nicht geschätzt)

- Sitzt der Rahmen, ist die Auslese auf realistischen Brettdarstellungen
  praktisch fehlerfrei: **99,9 % je Schnittpunkt, 98,6 % der Bretter exakt**.
- Die **Rahmensuche ist die offene Schwäche**: Sie trifft rund **72 %** der
  Bretter; die übrigen verfehlt sie meist um genau eine Gitterteilung, weil
  sie die Brettkante statt der ersten Gitterlinie nimmt. `--size` hilft
  dagegen nicht, weil der Fehler die Lage betrifft, nicht die Größe.
- Das klassische Backend meldet einen **weissen Stein im
  Schwarzweiss-Druckdiagramm als leeren Punkt** — sein Inneres ist dort
  exakt so hell wie das Papier. Ein Nachweis über die Steinkontur wurde
  erprobt und wieder entfernt, weil er "diese Farbe ist unsichtbar" nicht
  von "diese Farbe kommt nicht vor" trennt und im zweiten Fall Phantomsteine
  erzeugt. Für diese Vorlagen ist das U-Net zuständig.
- Ein rein synthetisch trainiertes Netz kann auf echten Fotos schwächer sein,
  als die synthetische Validierung nahelegt. Der Weg, echte gelabelte Fotos
  dazuzumischen, ist vorgesehen, aber nicht beschritten.

## Grenzen (bewusst und dokumentiert)

- Einfaches Ko statt Positional Superko (für legale SGF-Partien ausreichend;
  KataGo prüft serverseitig ohnehin regelkonform).
- Benson ist beweisbar korrekt, aber konservativ (Seki zählt nicht als
  unbedingt lebendig).
- Der Mock ist ein Distanz-Abkling-Einflussmodell — ausschließlich für
  Tests/CI/Pipeline-Demos, niemals für echtes Teaching.
- SGF: nur Hauptvariante; rechteckige Bretter werden nicht unterstützt.
- Bilderkennung: Die Rahmensuche verfehlt rund ein Viertel der Bretter um
  eine Gitterteilung (Details im Abschnitt „Stufen 1–2").
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
τ-Lokalität, Teaching end-to-end (Mock) inkl. Bereichsauswahl `-from/-to`,
der Stellungsvertrag des `vision`-Pakets, die Subprozess-Brücke gegen eine
Erkenner-Attrappe (inkl. Timeout und stderr-Weitergabe), `AnalyzePosition`
sowie der Bild-Upload des HTTP-Dienstes.

Die Python-Seite prüft `pytest` unter `vision/python/`: PNG-Normalisierung
(Alpha, Palette, Graustufen, 16 Bit), das kanonische Gitter, die Entzerrung
inkl. Degeneration bei achsparallelen Vorlagen, den JSON-Vertrag und den
Ende-zu-Ende-Roundtrip Render → Erkennung → Stellung. Alles ohne
Modellgewichte; die torch-Tests (Netzform, Training, ONNX-Export gegen torch)
überspringen sich, wenn torch fehlt.

## Quellen

- KataGo `docs/Analysis_Engine.md` und `cpp/configs/analysis_example.cfg`
  (Feld-, Ordnungs- und Perspektiven-Verträge; abgerufen 2026-08-05).
- D. B. Benson (1976): Life in the Game of Go. *Information Sciences* 10,
  17–29.
- A. L. Zobrist (1969): A model of visual organization for the game of Go;
  B. Bouzy (2003): Mathematical morphology applied to computer Go —
  Vorbilder des distanzgewichteten Einfluss-/Stärkemaßes.
- Interner Architekturbericht dieses Projekts (U-Net → KataGo-Ownership →
  LLM-Interpretationsschicht).
