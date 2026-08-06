# goteach — Go-Analyse mit Teaching pro Zug

Golang-Umsetzung der Stufen 3–6 der hybriden Go-Analysearchitektur
(symbolischer Brettzustand → Gruppensegmentierung → KataGo-Ownership →
situatives Stärkemaß) plus dem neuen Kernfeature **Teaching pro Zug**:
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
| `vision`           | Brücke zur Python/ONNX-Bilderkennung (Stufen 1–2), JSON-Stellungsformat |
| `internal/dotenv`  | Minimaler .env-Loader (Secrets nie in Flags oder Logs)                 |
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

Für Deployments (z. B. Vercel, das `main.go`, `cmd/api/main.go` oder
`cmd/server/main.go` als Entrypoint erwartet) gibt es denselben
Analyse-Kern als HTTP-Dienst. Die Logik liegt in `internal/server`;
`main.go` (Repo-Wurzel) und `cmd/server/main.go` sind identische
Wrapper — Vercels Root Directory muss auf die Repo-Wurzel zeigen,
damit nicht die CLI unter `cmd/goteach` gebaut wird.

```bash
go build -o goteach-server .
PORT=8080 ./goteach-server
```

| Endpunkt        | Funktion                                              |
|-----------------|-------------------------------------------------------|
| `GET /`         | Dienstinfo (JSON)                                     |
| `GET /healthz`  | Liveness-Check                                        |
| `POST /analyze` | SGF im Body → Teaching-Reports als JSON               |

Query-Parameter von `/analyze`: `visits` (Default 50, Deckel 1000),
`tau`, `from`, `to`, `rules`, `komi`.

Engine-Wahl über Umgebung: Sind `KATAGO_PATH` und `KATAGO_MODEL` gesetzt
(optional `KATAGO_CONFIG`, Default `analysis.cfg`), startet pro Anfrage
eine echte Engine. Andernfalls antwortet der Mock — die Antwort trägt
dann `"synthetic": true` und die Werte sind **keine echte Analyse**.

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

## Vision-Brücke (Stufen 1–2)

Homographie und preinformed U-Net verbleiben bewusst im Python/ONNX-Stack
(Trainings-/CV-Ökosystem). Deren Ausgabe wird als JSON übergeben:

```json
{ "size": 19, "komi": 7.5, "rows": ["...................", "...X...O...", "..."] }
```

`vision.FromJSON(...).Game()` liefert daraus eine analysierbare Stellung
(`initialStones`, ohne Zughistorie).

## Grenzen (bewusst und dokumentiert)

- Einfaches Ko statt Positional Superko (für legale SGF-Partien ausreichend;
  KataGo prüft serverseitig ohnehin regelkonform).
- Benson ist beweisbar korrekt, aber konservativ (Seki zählt nicht als
  unbedingt lebendig).
- Der Mock ist ein Distanz-Abkling-Einflussmodell — ausschließlich für
  Tests/CI/Pipeline-Demos, niemals für echtes Teaching.
- SGF: nur Hauptvariante; rechteckige Bretter werden nicht unterstützt.
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
τ-Lokalität, Teaching end-to-end (Mock) inkl. Bereichsauswahl `-from/-to`.

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
