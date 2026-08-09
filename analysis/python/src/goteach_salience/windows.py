"""Vom Salienzfeld zu Fenstern.

Ein Fenster ist eine Zusammenhangskomponente im Volumen aus Zugachse und
Brettflaeche: benachbarte Punkte, benachbarte Zuege, alle deutlich ueber dem
Grundrauschen. Das ist die Fensterung, die die Go-Seite danach mit Formen und
Zahlen fuellt.

Ohne Modellgewichte arbeitet das Modul auf der *beobachteten* Umwaelzung. Das
ist kein Notbehelf: Es macht die Pipeline ohne Training lauffaehig, dient dem
Netz als Vergleichsmassstab und ist der schnelle Pfad in der CI.
"""

from __future__ import annotations

import numpy as np

from .contract import Game, Window, to_gtp

# Anteil der Voxel, der als "ruhig" gilt und wegfaellt.
DEFAULT_QUANTILE = 0.97

# Ein Fenster, das mehr als diesen Anteil der Brettpunkte umfasst, ist kein
# Fenster mehr, sondern die halbe Partie. Solche Komponenten werden verworfen
# statt gemeldet — liefert das Modul daraufhin gar nichts, uebernimmt auf der
# Go-Seite die deterministische Fensterung. Ein zu grobes Fenster waere
# schlechter als keines: Es zoege die ganze Partie in einen einzigen Strang.
MAX_POINT_FRACTION = 0.25

# Kleinere Komponenten sind Rauschen, keine Geschichte.
MIN_VOXELS = 12


def observed_salience(game: Game) -> np.ndarray:
    """Die tatsaechliche Umwaelzung je Zug und Punkt, ohne Modell."""
    size = game.size
    out = np.zeros((len(game.turns), size, size), dtype=np.float32)

    for t in range(1, len(game.turns)):
        out[t] = np.abs(game.turns[t].ownership - game.turns[t - 1].ownership)

    return out


def find_windows(
    game: Game,
    salience: np.ndarray,
    quantile: float = DEFAULT_QUANTILE,
    top: int = 8,
) -> list[Window]:
    """Zerlegt das Salienzvolumen in Fenster, nach Gewicht sortiert."""
    if salience.ndim != 3 or salience.shape[0] < 2:
        return []

    threshold = float(np.quantile(salience, quantile))

    if threshold <= 1e-9:
        return []

    active = salience > threshold
    labels = _components(active)
    out: list[Window] = []

    limit = int(MAX_POINT_FRACTION * game.size * game.size)

    for voxels in labels:
        if len(voxels) < MIN_VOXELS:
            continue

        turns = [v[0] for v in voxels]
        points = sorted({(v[2], v[1]) for v in voxels})

        if len(points) > limit:
            continue

        weight = float(sum(salience[t, y, x] for t, y, x in voxels))

        out.append(
            Window(
                from_turn=min(turns),
                to_turn=max(turns),
                points=[to_gtp(x, y, game.size) for x, y in points],
                score=weight,
            )
        )

    out.sort(key=lambda w: w.score, reverse=True)

    if out:
        peak = out[0].score or 1.0

        for w in out:
            w.score = w.score / peak

    return out[:top]


def _components(active: np.ndarray) -> list[list[tuple[int, int, int]]]:
    """Zusammenhangskomponenten im Volumen (6er-Nachbarschaft).

    Die Zugachse zaehlt als Nachbarschaft wie die Brettachsen — genau dadurch
    wird aus einer Folge von Momentaufnahmen ein Abschnitt.
    """
    depth, height, width = active.shape
    seen = np.zeros_like(active, dtype=bool)
    out: list[list[tuple[int, int, int]]] = []

    neighbours = (
        (1, 0, 0), (-1, 0, 0), (0, 1, 0), (0, -1, 0), (0, 0, 1), (0, 0, -1),
    )

    for t0, y0, x0 in zip(*np.nonzero(active)):
        if seen[t0, y0, x0]:
            continue

        stack = [(int(t0), int(y0), int(x0))]
        seen[t0, y0, x0] = True
        component: list[tuple[int, int, int]] = []

        while stack:
            t, y, x = stack.pop()
            component.append((t, y, x))

            for dt, dy, dx in neighbours:
                nt, ny, nx = t + dt, y + dy, x + dx

                if not (0 <= nt < depth and 0 <= ny < height and 0 <= nx < width):
                    continue

                if active[nt, ny, nx] and not seen[nt, ny, nx]:
                    seen[nt, ny, nx] = True
                    stack.append((nt, ny, nx))

        out.append(component)

    return out
