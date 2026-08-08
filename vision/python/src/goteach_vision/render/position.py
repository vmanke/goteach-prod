"""Zufaellige, legal aussehende Stellungen.

Das Netz lernt Aussehen, nicht Strategie — deshalb genuegt es, Steine
zufaellig zu streuen und anschliessend Ketten ohne Freiheiten zu entfernen.
Ergebnis sind Stellungen, die es so auf einem Brett geben koennte, ohne dass
dafuer eine Regel-Engine noetig waere.
"""

from __future__ import annotations

import numpy as np

from ..contract import LABEL_BLACK, LABEL_EMPTY, LABEL_WHITE


def random_labels(
    rng: np.random.Generator, size: int, density: float | None = None
) -> np.ndarray:
    """Streut Steine und raeumt anschliessend tote Ketten ab."""
    if density is None:
        # Von "fast leer" (Eroeffnung) bis "dicht" (Endspiel).
        density = float(rng.uniform(0.02, 0.55))

    occupied = rng.random((size, size)) < density
    black = rng.random((size, size)) < 0.5

    labels = np.full((size, size), LABEL_EMPTY, dtype=np.int8)
    labels[occupied & black] = LABEL_BLACK
    labels[occupied & ~black] = LABEL_WHITE

    return strip_dead(labels)


def strip_dead(labels: np.ndarray) -> np.ndarray:
    """Entfernt Ketten ohne Freiheiten, bis sich nichts mehr aendert.

    Steine zu entfernen kann Nachbarketten nur *mehr* Freiheiten geben, die
    Schleife terminiert also schnell — zwei Durchlaeufe reichen praktisch
    immer, die Abbruchbedingung ist trotzdem exakt.
    """
    out = np.array(labels, dtype=np.int8, copy=True)

    while True:
        removed = False

        for chain, liberties in _chains(out):
            if liberties == 0:
                ys, xs = zip(*chain)
                out[list(ys), list(xs)] = LABEL_EMPTY
                removed = True

        if not removed:
            return out


def _chains(labels: np.ndarray):
    """Iteriert ueber (Kettenpunkte, Freiheitenzahl) beider Farben."""
    size = labels.shape[0]
    seen = np.zeros_like(labels, dtype=bool)

    for y in range(size):
        for x in range(size):
            color = labels[y, x]

            if color == LABEL_EMPTY or seen[y, x]:
                continue

            stack = [(y, x)]
            seen[y, x] = True
            chain: list[tuple[int, int]] = []
            liberties: set[tuple[int, int]] = set()

            while stack:
                cy, cx = stack.pop()
                chain.append((cy, cx))

                for ny, nx in ((cy - 1, cx), (cy + 1, cx), (cy, cx - 1), (cy, cx + 1)):
                    if not (0 <= ny < size and 0 <= nx < size):
                        continue

                    if labels[ny, nx] == LABEL_EMPTY:
                        liberties.add((ny, nx))

                    elif labels[ny, nx] == color and not seen[ny, nx]:
                        seen[ny, nx] = True
                        stack.append((ny, nx))

            yield chain, len(liberties)
