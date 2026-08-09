"""Sampler ueber beide Bilddomaenen.

Trainiert wird auf Screenshots *und* Fotos gemeinsam, weil auch erkannt
werden soll, was beide sind. Der Mischungsanteil ist einstellbar; die
Metriken werden spaeter getrennt je Domaene berichtet, damit ein Rueckschritt
in einer Domaene sich nicht hinter der anderen verstecken kann.

Train und Validierung ziehen aus disjunkten Seed-Bereichen. Weil jedes
Beispiel neu erzeugt wird, gibt es kein Leck zwischen den Mengen — dieselbe
Stellung zweimal zu sehen ist praktisch ausgeschlossen.
"""

from __future__ import annotations

import numpy as np

from ..contract import BOARD_SIZES
from ..features import build_input, target_mask
from ..geometry import CANONICAL_SIDE
from ..grid import DEFAULT_MARGIN, line_positions
from . import Sample
from .augment import apply_homography, perspective, photometric
from .photo import render_photo
from .screenshot import render_screenshot

# Seed-Bereiche; Validierung liegt weit hinter dem Training.
TRAIN_SEEDS = (0, 10_000_000)
VALIDATION_SEEDS = (900_000_000, 900_010_000)


def sample(
    seed: int,
    photo_ratio: float = 0.6,
    size: int | None = None,
    side: int = CANONICAL_SIDE,
    margin: float = DEFAULT_MARGIN,
) -> tuple[np.ndarray, np.ndarray, Sample]:
    """Erzeugt ein Trainingsbeispiel: Eingangstensor, Pixelmaske, Rohprobe."""
    rng = np.random.default_rng(seed)
    board_size = size or int(rng.choice(BOARD_SIZES))

    if rng.random() < photo_ratio:
        raw = render_photo(rng, board_size)
        raw = perspective(rng, raw, strength=float(rng.uniform(0.0, 0.09)))
        raw = photometric(rng, raw, strength=float(rng.uniform(0.2, 1.0)))
    else:
        # Screenshots sind achsparallel; nur milde photometrische Stoerungen
        # (Skalierung, Rauschen, Palettenreduktion) sind realistisch.
        raw = render_screenshot(rng, board_size)
        raw = photometric(rng, raw, strength=float(rng.uniform(0.0, 0.25)))

    warped, corners = _rectify(raw, board_size, side, margin)

    tensor = build_input(warped, board_size, margin)
    mask = target_mask(raw.labels, board_size, side, margin)

    return tensor, mask, Sample(
        image=warped,
        labels=raw.labels,
        corners=corners,
        size=board_size,
        domain=raw.domain,
    )


def _rectify(raw: Sample, size: int, side: int, margin: float):
    """Entzerrt mit der *bekannten* Homographie.

    Im Training wird die Geometrie nicht geschaetzt, sondern gesetzt: Stufe 1
    hat ihre eigene Verifikation, und das Netz soll die Steinerkennung lernen,
    nicht die Fehler des Detektors nachbilden. Ein kleiner Jitter bleibt
    trotzdem drin, damit das Netz die Restungenauigkeit echter Entzerrung
    vertraegt.
    """
    import cv2

    positions = line_positions(size, side, margin)
    first, last = positions[0], positions[-1]
    target = np.array(
        [[first, first], [last, first], [last, last], [first, last]],
        dtype=np.float32,
    )

    rng = np.random.default_rng(abs(int(raw.corners.sum() * 1000)) % (2**32))
    jitter = rng.normal(0.0, 0.004 * side, size=(4, 2)).astype(np.float32)

    matrix = cv2.getPerspectiveTransform(
        raw.corners.astype(np.float32), target + jitter
    )
    warped = cv2.warpPerspective(raw.image, matrix, (side, side), flags=cv2.INTER_AREA)

    return warped, apply_homography(matrix, raw.corners)


def seeds(span: tuple[int, int], count: int, offset: int = 0):
    """Deterministische Seed-Folge innerhalb eines Bereichs."""
    low, high = span

    return [low + (offset + i) % (high - low) for i in range(count)]
