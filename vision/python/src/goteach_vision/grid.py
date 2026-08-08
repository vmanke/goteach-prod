"""Kanonisches Gitter und die "preinformed" Geometriekanaele.

Nach der Entzerrung liegt das Brett achsparallel in einem Quadrat der
Kantenlaenge ``side``; die aeussersten Gitterlinien sitzen im Abstand
``margin * side`` vom Rand. Damit sind alle Schnittpunkte analytisch bekannt
— und genau dieses Wissen bekommt das Netz als Zusatzkanaele, statt es aus
den Pixeln rekonstruieren zu muessen.
"""

from __future__ import annotations

import numpy as np

# Anteil der Kantenlaenge, den das entzerrte Bild ausserhalb der aeussersten
# Gitterlinien mitfuehrt. Etwas Rand ist noetig, weil Randsteine ueber die
# Linie hinausragen.
DEFAULT_MARGIN = 0.06

# Radius der Auslese-Scheibe um einen Schnittpunkt, in Vielfachen der
# Gitterteilung. 0.35 bleibt sicher innerhalb eines Steins (Steine haben
# ueblich 0.45-0.48) und weit weg vom Nachbarschnittpunkt.
DISC_RADIUS = 0.35


def pitch(size: int, side: int, margin: float = DEFAULT_MARGIN) -> float:
    """Abstand benachbarter Gitterlinien in Pixeln."""
    return (side * (1.0 - 2.0 * margin)) / (size - 1)


def line_positions(size: int, side: int, margin: float = DEFAULT_MARGIN) -> np.ndarray:
    """Pixelkoordinaten der Gitterlinien (identisch fuer x und y)."""
    first = side * margin

    return first + np.arange(size, dtype=np.float64) * pitch(size, side, margin)


def intersections(size: int, side: int, margin: float = DEFAULT_MARGIN) -> np.ndarray:
    """Alle Schnittpunkte als (size*size, 2)-Array in (x, y), row-major."""
    pos = line_positions(size, side, margin)
    xs, ys = np.meshgrid(pos, pos, indexing="xy")

    return np.stack([xs.ravel(), ys.ravel()], axis=1)


def offsets_to_grid(
    size: int, side: int, margin: float = DEFAULT_MARGIN
) -> tuple[np.ndarray, np.ndarray]:
    """Signierter Abstand jedes Pixels zur naechsten Gitterlinie.

    Rueckgabe in Gittereinheiten, also in [-0.5, 0.5] im Brettinneren; jenseits
    der aeussersten Linie waechst der Betrag ueber 0.5 hinaus und markiert so
    den Randbereich.
    """
    p = pitch(size, side, margin)
    pos = line_positions(size, side, margin)
    coords = np.arange(side, dtype=np.float64) + 0.5

    # Differenz zu jeder Linie, davon die betragskleinste behalten.
    diff = coords[:, None] - pos[None, :]
    nearest = diff[np.arange(side), np.abs(diff).argmin(axis=1)] / p

    dx = np.broadcast_to(nearest[None, :], (side, side))
    dy = np.broadcast_to(nearest[:, None], (side, side))

    return dx.astype(np.float32), dy.astype(np.float32)


def preinformed_channels(
    size: int, side: int, margin: float = DEFAULT_MARGIN
) -> np.ndarray:
    """Die drei Geometriekanaele als (3, side, side) float32.

    Kanal 0 ist eine weiche Gittermaske, Kanal 1 und 2 sind die signierten
    Abstaende zur naechsten Linie in x- bzw. y-Richtung. Weil die Kanaele die
    Gitterteilung mitfuehren, tragen dieselben Gewichte 9x9, 13x13 und 19x19.
    """
    dx, dy = offsets_to_grid(size, side, margin)
    p = pitch(size, side, margin)

    # Linienbreite skaliert mit der Teilung, damit die Maske bei 9x9 nicht
    # duenner wirkt als bei 19x19.
    sigma = max(1.0, 0.05 * p) / p
    mask = np.maximum(
        np.exp(-0.5 * (dx / sigma) ** 2), np.exp(-0.5 * (dy / sigma) ** 2)
    )

    return np.stack([mask.astype(np.float32), dx, dy], axis=0)


def disc_masks(
    size: int, side: int, margin: float = DEFAULT_MARGIN, radius: float = DISC_RADIUS
) -> np.ndarray:
    """Boolesche (size*size, side, side)-Masken der Auslese-Scheiben.

    Nur fuer kleine ``side`` gedacht (Nachverarbeitung arbeitet stattdessen
    mit :func:`disc_indices`), aber praktisch fuer Tests und Debug-Overlays.
    """
    pts = intersections(size, side, margin)
    r = radius * pitch(size, side, margin)
    yy, xx = np.mgrid[0:side, 0:side]
    yy = yy + 0.5
    xx = xx + 0.5

    out = np.empty((pts.shape[0], side, side), dtype=bool)

    for i, (px, py) in enumerate(pts):
        out[i] = (xx - px) ** 2 + (yy - py) ** 2 <= r * r

    return out


def disc_indices(
    size: int, side: int, margin: float = DEFAULT_MARGIN, radius: float = DISC_RADIUS
) -> list[tuple[np.ndarray, np.ndarray]]:
    """Zeilen-/Spaltenindizes je Schnittpunkt-Scheibe.

    Deutlich sparsamer als volle Masken: pro Schnittpunkt wird nur das
    umgebende Quadrat betrachtet und darin der Kreis ausgeschnitten.
    """
    pts = intersections(size, side, margin)
    r = radius * pitch(size, side, margin)
    span = int(np.ceil(r)) + 1
    out: list[tuple[np.ndarray, np.ndarray]] = []

    for px, py in pts:
        cx, cy = int(round(px)), int(round(py))
        x0, x1 = max(0, cx - span), min(side, cx + span + 1)
        y0, y1 = max(0, cy - span), min(side, cy + span + 1)

        yy, xx = np.mgrid[y0:y1, x0:x1]
        inside = (xx + 0.5 - px) ** 2 + (yy + 0.5 - py) ** 2 <= r * r

        out.append((yy[inside], xx[inside]))

    return out


def cell_centres(size: int, side: int, margin: float = DEFAULT_MARGIN) -> np.ndarray:
    """Mittelpunkte der Gitterzellen als ((size-1)^2, 2)-Array in (x, y).

    Eine Zellmitte liegt 0.707 Gitterteilungen vom naechsten Schnittpunkt
    entfernt, ein Stein reicht nur 0.48 weit — dort ist also immer Brett zu
    sehen, egal wie dicht besetzt die Stellung ist. Genau deshalb ist die
    Zellmitte der verlaessliche Bezugspunkt fuer die Brettfarbe.
    """
    pos = line_positions(size, side, margin)
    mid = (pos[:-1] + pos[1:]) / 2.0
    xs, ys = np.meshgrid(mid, mid, indexing="xy")

    return np.stack([xs.ravel(), ys.ravel()], axis=1)


def discs_at(points: np.ndarray, side: int, radius: float):
    """Zeilen-/Spaltenindizes einer Kreisscheibe um jeden Punkt."""
    span = int(np.ceil(radius)) + 1
    out: list[tuple[np.ndarray, np.ndarray]] = []

    for px, py in points:
        cx, cy = int(round(px)), int(round(py))
        x0, x1 = max(0, cx - span), min(side, cx + span + 1)
        y0, y1 = max(0, cy - span), min(side, cy + span + 1)

        yy, xx = np.mgrid[y0:y1, x0:x1]
        inside = (xx + 0.5 - px) ** 2 + (yy + 0.5 - py) ** 2 <= radius * radius

        out.append((yy[inside], xx[inside]))

    return out


def annulus_indices(
    size: int,
    side: int,
    margin: float = DEFAULT_MARGIN,
    inner: float = 0.38,
    outer: float = 0.54,
) -> list[tuple[np.ndarray, np.ndarray]]:
    """Zeilen-/Spaltenindizes je Schnittpunkt-Ring.

    Der Ring liegt dort, wo der Rand eines Steins verlaeuft (Steinradius
    0.44-0.48 der Gitterteilung). Er ist der einzige Nachweis fuer einen
    weissen Stein auf weissem Grund — dessen Inneres unterscheidet sich vom
    Brett nicht, seine Kontur schon.
    """
    pts = intersections(size, side, margin)
    p = pitch(size, side, margin)
    r_in, r_out = inner * p, outer * p
    span = int(np.ceil(r_out)) + 1
    out: list[tuple[np.ndarray, np.ndarray]] = []

    for px, py in pts:
        cx, cy = int(round(px)), int(round(py))
        x0, x1 = max(0, cx - span), min(side, cx + span + 1)
        y0, y1 = max(0, cy - span), min(side, cy + span + 1)

        yy, xx = np.mgrid[y0:y1, x0:x1]
        d2 = (xx + 0.5 - px) ** 2 + (yy + 0.5 - py) ** 2
        ring = (d2 >= r_in * r_in) & (d2 <= r_out * r_out)

        out.append((yy[ring], xx[ring]))

    return out


def star_points(size: int) -> list[tuple[int, int]]:
    """Hoshi-Punkte der ueblichen Brettgroessen (x, y), 0-basiert."""
    if size < 7:
        return []

    edge = 3 if size >= 13 else 2
    center = size // 2
    coords = [edge, size - 1 - edge]

    pts = [(x, y) for y in coords for x in coords]

    if size % 2 == 1:
        pts.append((center, center))

        if size >= 19:
            pts.extend(
                [(edge, center), (size - 1 - edge, center),
                 (center, edge), (center, size - 1 - edge)]
            )

    return sorted(set(pts))
