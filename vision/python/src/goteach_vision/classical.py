"""Backend ohne ML: Steine aus der Helligkeit der Schnittpunkte.

Dieses Backend ist kein Notbehelf, sondern erfuellt drei Aufgaben: Es macht
die Pipeline ohne vortrainierte Gewichte lauffaehig, es ist der schnelle
CI-Pfad, und es dient dem U-Net als Sanity-Baseline. Auf Brettern mit
sichtbarem Farbkontrast — Holz hell wie dunkel, Dunkelmodus — ist es exakt.

**Bewusste Grenze.** Im Schwarzweiss-Lehrbuchdiagramm ist ein weisser Stein
innen exakt so hell wie das Papier; nur seine duenne Kontur verraet ihn.
Dieses Backend meldet ihn als leeren Punkt. Der naheliegende Ausweg — den
Steinrand als Kantenring nachzuweisen — wurde erprobt und wieder entfernt:
Er unterscheidet nicht zuverlaessig zwischen "diese Farbe ist unsichtbar"
und "diese Farbe kommt schlicht nicht vor", und im zweiten Fall ueberzieht
er ganze Brettbereiche mit Phantomsteinen. Ein fehlender Stein ist ein
sichtbarer Fehler; ein erfundener ist ein stiller. Fuer diese Vorlagen ist
das U-Net zustaendig.
"""

from __future__ import annotations

import cv2
import numpy as np

from .contract import LABEL_BLACK, LABEL_EMPTY, LABEL_WHITE
from .grid import DEFAULT_MARGIN, cell_centres, discs_at, pitch
from .postprocess import disc_statistic, line_pixels

# Mindestabstand in Grauwerten, ab dem ein Helligkeitsunterschied als
# Farbunterschied zaehlt und nicht als Textur.
MIN_SEPARATION = 16.0

# Anteil des beobachteten Kontrastumfangs, der als Schwelle dient.
THRESHOLD_FRACTION = 0.45

# Auslese-Radius in Vielfachen der Gitterteilung. Kleiner als der Vorgabewert
# in grid.py, damit auch bei leicht danebenliegendem Gitter nur Steininneres
# in die Scheibe faellt.
INNER_RADIUS = 0.30

# Radius der Brettproben in den Zellmitten.
BOARD_PROBE_RADIUS = 0.18


def classify(
    warped: np.ndarray, size: int, margin: float = DEFAULT_MARGIN
) -> tuple[np.ndarray, np.ndarray]:
    """Klassifiziert alle Schnittpunkte eines entzerrten Bildes.

    Rueckgabe: (size, size)-Labels und die zugehoerige Konfidenz in [0, 1].
    """
    gray = cv2.cvtColor(warped, cv2.COLOR_RGB2GRAY).astype(np.float64)
    grid_lines = line_pixels(size, gray.shape[0], margin)
    interior = disc_statistic(
        gray, size, margin, radius=INNER_RADIUS, exclude=grid_lines
    )

    # Bezugspunkt ist die Brettfarbe in den Zellmitten, nicht der Median ueber
    # die Schnittpunkte: Sobald mehr als die Haelfte der Punkte besetzt ist,
    # wandert so ein Median auf die Steinfarbe, und leere Punkte erscheinen
    # ploetzlich "dunkel" — sie wuerden reihenweise zu schwarzen Steinen. In
    # den Zellmitten liegt dagegen nie ein Stein.
    board = _board_level(gray, size, margin)
    deviation = interior - board

    # Schwellen aus dem tatsaechlich beobachteten Kontrast ableiten: ein
    # Dunkelmodus-Brett trennt Schwarz viel schwaecher vom Untergrund als
    # helles Holz, eine feste Schwelle wuerde dort alles verwerfen.
    white_threshold = _threshold(deviation[deviation > 0])
    black_threshold = _threshold(-deviation[deviation < 0])

    labels = np.full(interior.shape, LABEL_EMPTY, dtype=np.int8)
    labels[deviation >= white_threshold] = LABEL_WHITE
    labels[deviation <= -black_threshold] = LABEL_BLACK

    confidence = _confidence(deviation, white_threshold, black_threshold)

    return labels.reshape(size, size), confidence.reshape(size, size)


def _threshold(positive: np.ndarray) -> float:
    """Schwelle aus dem oberen Rand einer einseitigen Abweichungsverteilung.

    Unendlich bedeutet: In diese Richtung gibt es keinen nennenswerten
    Kontrast, also auch keine Steine dieser Farbe. Das ist der Normalfall auf
    einem Brett, das nur eine Farbe zeigt — und es muss folgenlos bleiben,
    sonst wird Textur zu Steinen.
    """
    if positive.size == 0:
        return np.inf

    extent = float(np.percentile(positive, 90))

    if extent < MIN_SEPARATION:
        return np.inf

    return max(MIN_SEPARATION, THRESHOLD_FRACTION * extent)


def _confidence(
    deviation: np.ndarray, white_threshold: float, black_threshold: float
) -> np.ndarray:
    """Abstand zur naechsten Entscheidungsgrenze, auf [0, 1] normiert."""
    to_white = (
        np.abs(deviation - white_threshold)
        if np.isfinite(white_threshold)
        else np.full(deviation.shape, np.inf)
    )
    to_black = (
        np.abs(deviation + black_threshold)
        if np.isfinite(black_threshold)
        else np.full(deviation.shape, np.inf)
    )

    reference = min(
        white_threshold if np.isfinite(white_threshold) else np.inf,
        black_threshold if np.isfinite(black_threshold) else np.inf,
    )

    if not np.isfinite(reference) or reference <= 0:
        return np.ones(deviation.shape)

    return np.clip(np.minimum(to_white, to_black) / reference, 0.0, 1.0)


def _board_level(gray: np.ndarray, size: int, margin: float) -> float:
    """Grauwert des unbedeckten Bretts, gemessen in den Zellmitten."""
    side = gray.shape[0]
    radius = BOARD_PROBE_RADIUS * pitch(size, side, margin)
    samples = [
        np.median(gray[ys, xs])
        for ys, xs in discs_at(cell_centres(size, side, margin), side, radius)
        if len(ys)
    ]

    if not samples:
        return float(np.median(gray))

    return float(np.median(samples))
