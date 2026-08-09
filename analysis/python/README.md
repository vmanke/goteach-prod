# goteach-salience — gelerntes Salienzmodul

Aus dem Verlauf einer Partie werden die Fenster bestimmt, in denen sich noch
etwas entscheidet: Bereiche aus Zugspanne und Brettpunkten, nach Brisanz
sortiert. Die Go-Seite füllt sie danach mit benannten Formen und
nachgerechneten Zahlen.

**Die Arbeitsteilung ist der Punkt.** Ein gelerntes Modell darf hier
bestimmen, *worüber* geredet wird — niemals, *was* dabei behauptet wird. Alle
Zahlen im Lehrtext kommen weiter deterministisch aus KataGo, Benson und
Brettlogik. Fällt dieses Modul aus, übernimmt auf der Go-Seite die
deterministische Fensterung; es ist eine Verbesserung, keine Voraussetzung.

## Installation

```bash
pip install -e ".[dev]"              # Fensterung ohne ML
pip install -e ".[dev,onnx]"         # zusätzlich Netz-Inferenz
pip install -e ".[dev,onnx,train]"   # zusätzlich Training und Export
```

## Kommandozeile

```bash
# Fenster bestimmen; stdout ist ausschließlich der JSON-Vertrag
python -m goteach_salience score partie.json
cat partie.json | python -m goteach_salience score - --weights netz.onnx

# Trainieren und exportieren
python -m goteach_salience train --steps 400 --out netz.pt --onnx netz.onnx
python -m goteach_salience train --tiny            # Rauchtest
python -m goteach_salience export --state netz.pt --out netz.onnx
```

Anbindung an goteach:

```bash
export GOTEACH_SALIENCE_CMD="python3 -m goteach_salience score -"
goteach -sgf partie.sgf -mock
```

## Verträge

Eingabe je Stellung die Brettzeilen (`.` leer, `X` schwarz, `O` weiß, Zeile 0
oben — identisch zum Vertrag der Bilderkennung) und das Ownership-Feld:

```json
{"size": 19, "turns": [{"rows": ["...", "..."], "ownership": [0.1, -0.4]}]}
```

Ausgabe die Fenster:

```json
{"windows": [{"fromTurn": 60, "toTurn": 88, "points": ["Q4", "R4"], "score": 1.0}]}
```

## Das Netz

Encoder-Decoder mit **bidirektionaler Rückkopplung**: An jeder Ebene trifft
ein Signal von unten (Kodierer-Richtung) auf eines von oben
(Dekoder-Richtung). Welches eine Ebene in einem Durchlauf bekommt, wird an
der Nichtlinearität stochastisch gezogen und über ein gelerntes Tor skaliert.
In der Literatur ist das die Familie um Predictive Coding und
Feedback-Netze; Deep Equilibrium Models sind die sauberste Formulierung.

Zwei Festlegungen sind keine Geschmacksfragen:

- **Feste Zahl von Durchläufen** (drei). Ein unbestimmter Fixpunkt hätte
  keinen definierten ONNX-Graphen; wer echte Konvergenz will, nimmt DEQ mit
  implizitem Gradienten statt zu entrollen.
- **Erwartungswert statt Stichprobe zur Laufzeit.** Im Training wird die
  Richtung gezogen, in der Inferenz werden beide mit ihrer Wahrscheinlichkeit
  gewichtet — genau wie Dropout. Ohne das ergäbe dieselbe Partie zweimal
  analysiert nicht dasselbe Ergebnis. Der Export prüft das ausdrücklich nach
  und bricht ab, wenn der stochastische Zweig in den Graphen geraten ist.

Eingang ist `[HISTORY × 4, 19, 19]` — je Zeitschritt schwarze Steine, weiße
Steine, Ownership und dessen Änderung, über vier Züge Vergangenheit. Das ist
das „2D mit Historie".

## Das Lernsignal

Für „narrativ interessant" gibt es keine Ground Truth, und es kann keine
geben, ohne dass jemand hunderte Partien von Hand markiert. Das Netz lernt
deshalb eine Größe, die in jeder Partie nachprüfbar ist: **wie stark sich die
Zugehörigkeit eines Punktes in den nächsten zwölf Zügen noch verändert** —
vorhergesagt aus der jetzigen Stellung. Das Netz soll Brisanz erkennen, bevor
sie sich auszahlt. Verlust: Huber, keine Labels nötig.

## Tests

```bash
pytest -q
```

Läuft ohne Modellgewichte durch: Vertrag, Merkmale und Fensterung sind ohne
ML prüfbar. Die torch-Tests überspringen sich, wenn torch fehlt; sie prüfen
unter anderem, dass die Inferenz deterministisch ist, das Training dagegen
tatsächlich streut — ohne beides wäre die Konstruktion sinnlos.

## Stand

Ehrlich gesagt: Die deterministische Fensterung auf der Go-Seite liefert
derzeit die besseren Stränge. Dieses Modul ist gebaut, geprüft und
angebunden, aber auf synthetischen Partien trainiert; für belastbare Fenster
bräuchte es echte Partien mit echter KataGo-Ownership. Bis dahin ist der
Kopplungsgraph der Produktionspfad und dieses Modul die Alternative, die man
einschalten kann.
