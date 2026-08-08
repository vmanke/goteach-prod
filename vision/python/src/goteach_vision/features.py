"""Der Eingangstensor des Netzes — eine einzige Quelle der Wahrheit.

Training, ONNX-Export und Inferenz muessen exakt dieselben Kanaele in
derselben Reihenfolge und Skalierung bauen. Deshalb steht das hier und
nirgends sonst; jede Abweichung waere ein stiller Genauigkeitsverlust, den
kein Test bemerkt, weil beide Seiten fuer sich konsistent bleiben.
"""

from __future__ import annotations

import numpy as np

from .grid import DEFAULT_MARGIN, discs_at, intersections, pitch, preinformed_channels

# 3 Farbkanaele plus die drei Geometriekanaele aus grid.preinformed_channels.
INPUT_CHANNELS = 6

# Steinradius in Vielfachen der Gitterteilung, wie ihn die Renderer zeichnen.
STONE_RADIUS = 0.46


def build_input(
    warped: np.ndarray, size: int, margin: float = DEFAULT_MARGIN
) -> np.ndarray:
    """Baut (6, side, side) float32 aus dem entzerrten RGB-Bild.

    Kanaele 0-2: RGB, zentriert auf [-0.5, 0.5].
    Kanaele 3-5: Gittermaske und die signierten Abstaende zur naechsten
    Linie — das "preinformed" des U-Nets. Sie tragen die Gitterteilung mit,
    weshalb dieselben Gewichte 9x9, 13x13 und 19x19 bedienen.
    """
    side = warped.shape[0]
    colour = np.asarray(warped, dtype=np.float32).transpose(2, 0, 1) / 255.0 - 0.5

    return np.concatenate(
        [colour, preinformed_channels(size, side, margin)], axis=0
    ).astype(np.float32)


def target_mask(
    labels: np.ndarray, size: int, side: int, margin: float = DEFAULT_MARGIN
) -> np.ndarray:
    """Pixelweise Klassenmaske (side, side) int64 aus den Feldlabels.

    Ein Stein bedeckt die Kreisscheibe um seinen Schnittpunkt; alles andere
    ist "leer". Der Radius entspricht dem gerenderten Stein, damit die
    Segmentierung dieselbe Geometrie lernt, die spaeter ausgelesen wird.
    """
    mask = np.zeros((side, side), dtype=np.int64)
    points = intersections(size, side, margin)
    radius = STONE_RADIUS * pitch(size, side, margin)
    flat = np.asarray(labels).ravel()

    for index, (ys, xs) in enumerate(discs_at(points, side, radius)):
        if flat[index] and len(ys):
            mask[ys, xs] = int(flat[index])

    return mask
