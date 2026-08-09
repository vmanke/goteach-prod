"""Overlay zur Sichtpruefung der Erkennung.

Zeigt das entzerrte Brett mit dem angenommenen Gitter und dem erkannten
Symbol je Schnittpunkt. Wenn etwas schiefgeht, ist auf einen Blick zu sehen,
ob die Geometrie danebenlag oder die Steinerkennung.
"""

from __future__ import annotations

import cv2
import numpy as np

from .contract import LABEL_BLACK, LABEL_EMPTY, LABEL_WHITE
from .grid import intersections, pitch

# Gitter blau, Schwarz rot, Weiss gruen, unsichere Punkte gelb (BGR-frei:
# hier wird direkt in RGB gezeichnet).
GRID_COLOUR = (70, 130, 255)
BLACK_COLOUR = (255, 80, 80)
WHITE_COLOUR = (80, 220, 120)
LOW_CONFIDENCE_COLOUR = (255, 210, 0)


def overlay(detection, low_confidence: float = 0.35) -> np.ndarray:
    """Malt Gitter und Erkennung in das entzerrte Bild."""
    geo = detection.geometry
    canvas = np.ascontiguousarray(geo.warped.copy())
    size, side, margin = geo.size, geo.side, geo.margin

    points = intersections(size, side, margin)
    step = pitch(size, side, margin)
    radius = max(2, int(round(0.3 * step)))

    positions = points.reshape(size, size, 2)
    labels = detection.position.to_labels()

    for index in range(size):
        _line(canvas, positions[index, 0], positions[index, -1])
        _line(canvas, positions[0, index], positions[-1, index])

    for y in range(size):
        for x in range(size):
            centre = (int(round(positions[y, x, 0])), int(round(positions[y, x, 1])))
            label = labels[y, x]
            weak = detection.confidence[y, x] < low_confidence

            if label == LABEL_EMPTY:
                if weak:
                    cv2.drawMarker(
                        canvas, centre, LOW_CONFIDENCE_COLOUR, cv2.MARKER_TILTED_CROSS,
                        radius, 1,
                    )

                continue

            colour = BLACK_COLOUR if label == LABEL_BLACK else WHITE_COLOUR
            cv2.circle(canvas, centre, radius, colour, 2)

            if weak:
                cv2.circle(canvas, centre, radius + 3, LOW_CONFIDENCE_COLOUR, 1)

    return canvas


def _line(canvas, start, end) -> None:
    cv2.line(
        canvas,
        (int(round(start[0])), int(round(start[1]))),
        (int(round(end[0])), int(round(end[1]))),
        GRID_COLOUR,
        1,
    )
