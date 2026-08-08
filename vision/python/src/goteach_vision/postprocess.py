"""Von der Pixelebene zur symbolischen Stellung.

Beide Backends — das U-Net und der klassische Pfad — enden hier: pro
Schnittpunkt wird eine Kreisscheibe ausgelesen, daraus entsteht ein Symbol
und ein Konfidenzwert. Die Scheibe ist bewusst kleiner als ein Stein, damit
weder Nachbarsteine noch der Brettrand hineinragen.
"""

from __future__ import annotations

import numpy as np

from .contract import LABEL_EMPTY
from .grid import DEFAULT_MARGIN, DISC_RADIUS, disc_indices


def pool_logits(
    logits: np.ndarray,
    size: int,
    margin: float = DEFAULT_MARGIN,
    radius: float = DISC_RADIUS,
) -> np.ndarray:
    """Mittelt (K, H, W)-Logits ueber die Scheibe je Schnittpunkt.

    Rueckgabe (size*size, K) in row-major Ordnung — dieselbe Reihenfolge, die
    KataGo fuer Ownership benutzt.
    """
    side = logits.shape[1]
    discs = disc_indices(size, side, margin, radius)
    out = np.empty((len(discs), logits.shape[0]), dtype=np.float64)

    for i, (ys, xs) in enumerate(discs):
        if len(ys) == 0:
            out[i] = 0.0
        else:
            out[i] = logits[:, ys, xs].mean(axis=1)

    return out


def decide(scores: np.ndarray, size: int) -> tuple[np.ndarray, np.ndarray]:
    """Argmax je Schnittpunkt plus Konfidenz als Softmax-Abstand.

    Die Konfidenz ist der Abstand zwischen bester und zweitbester Klasse nach
    Softmax; sie faellt genau dort, wo es darauf ankommt — bei halb verdeckten
    oder unscharfen Steinen.
    """
    shifted = scores - scores.max(axis=1, keepdims=True)
    probability = np.exp(shifted)
    probability /= probability.sum(axis=1, keepdims=True)

    ordered = np.sort(probability, axis=1)
    confidence = ordered[:, -1] - ordered[:, -2]

    labels = scores.argmax(axis=1).astype(np.int8).reshape(size, size)

    return labels, confidence.reshape(size, size)


def disc_statistic(
    image: np.ndarray,
    size: int,
    margin: float = DEFAULT_MARGIN,
    radius: float = DISC_RADIUS,
    exclude: np.ndarray | None = None,
) -> np.ndarray:
    """Median je Schnittpunkt-Scheibe eines (H, W)-Bildes.

    ``exclude`` blendet Pixel aus — gedacht fuer die Gitterlinien, die jede
    Scheibe kreuzen. Der Median allein genuegt dafuer *nicht*: Stammt die
    Vorlage aus einem kleinen Bild, wird sie zur kanonischen Groesse
    hochskaliert, und die Linien verschmieren dabei auf ein Vielfaches ihrer
    Breite. Dann bedeckt das Kreuz mehr als die halbe Scheibe und zieht auch
    den Median mit — leere Punkte erscheinen dunkler als das Brett und werden
    zu schwarzen Steinen. Die Gitterlage ist bekannt, also wird sie genutzt.
    """
    side = image.shape[0]
    discs = disc_indices(size, side, margin, radius)
    out = np.empty(len(discs), dtype=np.float64)

    for i, (ys, xs) in enumerate(discs):
        if len(ys) == 0:
            out[i] = np.nan

            continue

        values = image[ys, xs]

        if exclude is not None:
            keep = ~exclude[ys, xs]

            # Nur ausblenden, solange genug Pixel uebrig bleiben.
            if keep.sum() >= max(8, 0.2 * len(values)):
                values = values[keep]

        out[i] = np.median(values)

    return out


def line_pixels(
    size: int, side: int, margin: float = DEFAULT_MARGIN, width: float = 0.09
) -> np.ndarray:
    """Boolesche Maske der Pixel nahe einer Gitterlinie.

    ``width`` ist der halbe Abstand in Gittereinheiten; grosszuegig gewaehlt,
    damit auch weichgezeichnete Linien vollstaendig hineinfallen.
    """
    from .grid import offsets_to_grid

    dx, dy = offsets_to_grid(size, side, margin)

    return (np.abs(dx) < width) | (np.abs(dy) < width)


def empty_board(size: int) -> np.ndarray:
    return np.full((size, size), LABEL_EMPTY, dtype=np.int8)
