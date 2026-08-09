# goteach-vision — Stufen 1–2

Aus einem PNG eines Go-Bretts wird eine symbolische Stellung im
Austauschformat der Go-Seite (`vision/adapter.go`). Zwei Stufen:

1. **Homographie** — Brett finden und entzerren. Klassisches CV, kein Netz.
2. **Preinformed U-Net** — Steine erkennen, mit der Gittergeometrie als
   zusätzlichen Eingangskanälen.

Ein Einstiegspunkt für beide Bilddomänen: saubere Screenshots und Diagramme
ebenso wie echte Brettfotos. Bei achsparallelen Vorlagen degeneriert die
Homographie rechnerisch zu Skalierung plus Zuschnitt — es gibt keinen
zweiten Codepfad.

## Installation

```bash
pip install -e ".[dev]"                # Erkennung ohne ML
pip install -e ".[dev,onnx]"           # zusätzlich U-Net-Inferenz
pip install -e ".[dev,onnx,train]"     # zusätzlich Training und ONNX-Export
```

## Kommandozeile

```bash
# Erkennen; stdout ist ausschließlich der JSON-Vertrag, alles andere stderr
python -m goteach_vision detect brett.png --size 19 --komi 7.5
python -m goteach_vision detect brett.png --weights netz.onnx --debug overlay.png
cat brett.png | python -m goteach_vision detect -

# Synthetisches Brett erzeugen (Ground Truth optional daneben)
python -m goteach_vision render --out brett.png --domain photo --seed 7 \
                                --labels brett.json

# Trainieren und exportieren
python -m goteach_vision train --steps 2000 --out netz.pt --onnx netz.onnx
python -m goteach_vision train --tiny            # CPU-Rauchtest, unter einer Minute
python -m goteach_vision export --state netz.pt --out netz.onnx
```

Exitcodes: `0` erkannt, `1` Fehler, `2` unterhalb von `--min-confidence`.

## Verträge

Eingabe ist **PNG**. Alpha wird gegen Weiß komponiert, Paletten- und
Graustufenbilder werden nach RGB gewandelt, 16-Bit-Kanäle auf 8 Bit
reduziert — keine PNG-Eigenheit erreicht das Netz (`pngio.load_rgb`).

Ausgabe auf stdout, exakt so wie `vision.FromJSON` in Go es liest:

```json
{"size": 19, "komi": 7.5, "rows": ["...................", "...X...O..........."]}
```

`rows[0]` ist die oberste Brettzeile; `.` leer, `X` schwarz, `O` weiß.

## Trainingsdaten

Es gibt keinen echten Datensatz; alles Material entsteht prozedural
(`render/`). Der Screenshot-Renderer zeichnet Diagramme in wechselnden
Paletten, der Foto-Renderer Holzmaserung, kugelschattierte Steine mit
Glanzlicht, Schlagschatten und Beleuchtungsgradienten. Die Augmentierung
ergänzt Perspektive, Unschärfe, Rauschen, Farbtemperatur, Verdeckungen und
gelegentlich Palettenquantisierung (PNG-8) — JPEG-Artefakte bewusst nicht,
das Eingabeformat ist PNG.

Train und Validierung ziehen aus disjunkten Seed-Bereichen; Metriken werden
getrennt je Domäne berichtet, damit ein Rückschritt in einer Domäne sich
nicht hinter der anderen verstecken kann.

## Tests

```bash
pytest -q
```

Läuft ohne Modellgewichte durch: Der klassische Pfad genügt für den
Ende-zu-Ende-Roundtrip. Die torch-Tests überspringen sich, wenn torch fehlt.

Gemessen und im Wurzel-README dokumentiert: Bei getroffenem Rahmen ist die
Auslese auf realistischen Brettdarstellungen praktisch fehlerfrei; die
Rahmensuche selbst trifft rund 72 % der Bretter und ist damit die offene
Schwäche der Pipeline.
