"""Domaene "screenshot": sauber gerenderte Bretter.

Gemeint sind Diagramme und Screenshots von Go-Servern oder aus Lehrbuechern:
achsparallel, gleichmaessig beleuchtet, scharfe Linien. Diese Domaene ist der
CI-Pfad — hier muss die Erkennung exakt sein, nicht nur gut.
"""

from __future__ import annotations

import numpy as np
from PIL import Image, ImageDraw

from ..contract import LABEL_BLACK, LABEL_EMPTY, LABEL_WHITE
from ..grid import star_points
from . import Sample
from .position import random_labels

# Supersampling-Faktor: gezeichnet wird gross, ausgegeben klein — das ergibt
# das Antialiasing echter UI-Renderer.
SUPERSAMPLE = 3

# (Brett, Linie, Aussenrand, Schwarz, Weiss) als RGB.
PALETTES = (
    ((222, 184, 135), (60, 40, 20), (245, 240, 230), (20, 20, 22), (246, 246, 244)),
    ((219, 177, 116), (48, 32, 16), (233, 222, 205), (24, 22, 20), (250, 250, 250)),
    ((255, 255, 255), (0, 0, 0), (255, 255, 255), (0, 0, 0), (255, 255, 255)),
    ((45, 45, 50), (140, 140, 150), (25, 25, 28), (12, 12, 15), (235, 235, 238)),
    ((236, 214, 168), (70, 55, 40), (250, 248, 244), (28, 26, 24), (252, 252, 250)),
)


def render_screenshot(
    rng: np.random.Generator,
    size: int,
    labels: np.ndarray | None = None,
    palette: int | None = None,
) -> Sample:
    """Rendert ein Brett als sauberes Diagramm."""
    if labels is None:
        labels = random_labels(rng, size)

    if palette is None:
        palette = int(rng.integers(len(PALETTES)))

    board, line, outside, black, white = PALETTES[palette % len(PALETTES)]

    pitch = float(rng.uniform(16.0, 42.0))
    margin = pitch * float(rng.uniform(0.55, 1.5))
    pad = pitch * float(rng.uniform(0.0, 1.2))

    span = (size - 1) * pitch
    side = int(round(span + 2 * margin + 2 * pad))

    # Position der obersten/linkesten Gitterlinie im fertigen Bild.
    origin = pad + margin

    ss = SUPERSAMPLE
    img = Image.new("RGB", (side * ss, side * ss), outside)
    draw = ImageDraw.Draw(img)

    # Brettflaeche (der Aussenrand bleibt in "outside" stehen).
    draw.rectangle(
        [pad * ss, pad * ss, (side - pad) * ss - 1, (side - pad) * ss - 1],
        fill=board,
    )

    width = max(ss, int(round(0.035 * pitch * ss)))

    for i in range(size):
        at = (origin + i * pitch) * ss
        draw.line([(origin * ss, at), ((origin + span) * ss, at)], fill=line, width=width)
        draw.line([(at, origin * ss), (at, (origin + span) * ss)], fill=line, width=width)

    star_r = max(ss, int(round(0.085 * pitch * ss)))

    for sx, sy in star_points(size):
        cx, cy = (origin + sx * pitch) * ss, (origin + sy * pitch) * ss
        draw.ellipse([cx - star_r, cy - star_r, cx + star_r, cy + star_r], fill=line)

    stone_r = float(rng.uniform(0.44, 0.48)) * pitch * ss
    rim = _rim_colors(board, black, white)

    for y in range(size):
        for x in range(size):
            if labels[y, x] == LABEL_EMPTY:
                continue

            fill = black if labels[y, x] == LABEL_BLACK else white
            edge = rim[0] if labels[y, x] == LABEL_BLACK else rim[1]
            cx, cy = (origin + x * pitch) * ss, (origin + y * pitch) * ss

            draw.ellipse(
                [cx - stone_r, cy - stone_r, cx + stone_r, cy + stone_r],
                fill=fill,
                outline=edge,
                width=max(1, int(round(0.03 * pitch * ss))) if edge else 0,
            )

    img = img.resize((side, side), Image.LANCZOS)

    corners = np.array(
        [
            [origin, origin],
            [origin + span, origin],
            [origin + span, origin + span],
            [origin, origin + span],
        ],
        dtype=np.float64,
    )

    return Sample(
        image=np.asarray(img, dtype=np.uint8),
        labels=np.asarray(labels, dtype=np.int8),
        corners=corners,
        size=size,
        domain="screenshot",
    )


def _rim_colors(board, black, white):
    """Konturfarben, aber nur wo der Stein sonst mit dem Brett verschmaeme.

    Dunkelmodus-Oberflaechen zeichnen schwarze Steine mit feinem Rand, weil
    sie sonst unsichtbar waeren; helle Bretter brauchen das nur fuer Weiss.
    """
    def lum(c):
        return 0.299 * c[0] + 0.587 * c[1] + 0.114 * c[2]

    bg = lum(board)
    black_rim = (90, 90, 96) if abs(lum(black) - bg) < 60 else None
    white_rim = (70, 70, 74) if abs(lum(white) - bg) < 60 else None

    return black_rim, white_rim


__all__ = ["render_screenshot", "PALETTES", "LABEL_BLACK", "LABEL_WHITE"]
