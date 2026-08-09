"""Domaene "photo": simulierte Brettfotos.

Holzmaserung, Kugelschattierung, Glanzlichter, Schlagschatten und ein
Beleuchtungsgradient — alles prozedural, damit Labels exakt bleiben. Die
perspektivische Verzerrung kommt spaeter in :mod:`.augment` dazu; hier
entsteht zunaechst die frontale Ansicht.
"""

from __future__ import annotations

import cv2
import numpy as np

from ..contract import LABEL_BLACK, LABEL_EMPTY
from ..grid import star_points
from . import Sample
from .position import random_labels

# Holztoene realer Bretter (Kaya hell bis Katsura dunkel), RGB.
WOOD_TONES = (
    (214, 176, 122),
    (198, 156, 104),
    (228, 194, 146),
    (176, 132, 86),
    (236, 208, 166),
)


def render_photo(
    rng: np.random.Generator,
    size: int,
    labels: np.ndarray | None = None,
) -> Sample:
    """Rendert ein Brett als frontales, foto-artiges Bild."""
    if labels is None:
        labels = random_labels(rng, size)

    pitch = float(rng.uniform(20.0, 46.0))
    margin = pitch * float(rng.uniform(0.6, 1.4))
    pad = pitch * float(rng.uniform(0.3, 1.6))

    span = (size - 1) * pitch
    side = int(round(span + 2 * margin + 2 * pad))
    origin = pad + margin

    yy, xx = np.mgrid[0:side, 0:side].astype(np.float32)
    yy += 0.5
    xx += 0.5

    img = _wood(rng, side, xx, yy)
    img = _table(rng, img, xx, yy, pad, side)
    img = _grid(rng, img, xx, yy, size, origin, pitch)

    # Lichtrichtung: normiert, zeigt von der Lichtquelle zum Brett.
    theta = float(rng.uniform(0, 2 * np.pi))
    light = np.array([np.cos(theta) * 0.55, np.sin(theta) * 0.55, 0.63], dtype=np.float32)
    light /= np.linalg.norm(light)

    img = _shadows(img, labels, origin, pitch, light, xx, yy, rng)
    img = _stones(img, labels, origin, pitch, light, xx, yy, rng)
    img = _lighting(rng, img, xx, yy, side)

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
        image=np.clip(img, 0, 255).astype(np.uint8),
        labels=np.asarray(labels, dtype=np.int8),
        corners=corners,
        size=size,
        domain="photo",
    )


def _fbm(rng: np.random.Generator, side: int, octaves: int = 4) -> np.ndarray:
    """Fraktales Rauschen als Summe geglaetteter Zufallsfelder."""
    out = np.zeros((side, side), dtype=np.float32)
    amp = 1.0

    for octave in range(octaves):
        scale = 2 ** (octave + 1)
        small = rng.standard_normal((max(2, side // (2 ** (octaves - octave))),) * 2)
        small = small.astype(np.float32)
        up = cv2.resize(small, (side, side), interpolation=cv2.INTER_CUBIC)
        out += amp * up
        amp *= 0.5

    return out / (np.abs(out).max() + 1e-6)


def _wood(rng: np.random.Generator, side: int, xx, yy) -> np.ndarray:
    """Grundflaeche mit Maserung: Sinusringe, vom Rauschen verzerrt."""
    tone = np.array(WOOD_TONES[rng.integers(len(WOOD_TONES))], dtype=np.float32)
    angle = float(rng.uniform(0, np.pi))
    freq = float(rng.uniform(0.02, 0.07))

    axis = xx * np.cos(angle) + yy * np.sin(angle)
    warp = _fbm(rng, side) * float(rng.uniform(8.0, 26.0))
    grain = np.sin((axis + warp) * freq * 2 * np.pi)

    # Maserung wirkt als multiplikative Helligkeitsmodulation, plus feines
    # Korn fuer die Textur zwischen den Ringen.
    modulation = 1.0 + 0.09 * grain + 0.03 * _fbm(rng, side, octaves=5)

    return tone[None, None, :] * modulation[:, :, None]


def _table(rng, img, xx, yy, pad, side):
    """Unterlage ausserhalb des Bretts (Tisch, Matte)."""
    if pad < 1.0:
        return img

    table = np.array(
        [rng.uniform(40, 150), rng.uniform(35, 140), rng.uniform(30, 130)],
        dtype=np.float32,
    )
    outside = (xx < pad) | (yy < pad) | (xx > side - pad) | (yy > side - pad)
    texture = 1.0 + 0.06 * _fbm(rng, side, octaves=3)

    out = img.copy()
    out[outside] = (table[None, :] * texture[outside][:, None])

    return out


def _grid(rng, img, xx, yy, size, origin, pitch):
    """Gitterlinien als weiche Abdunklung, plus Hoshi-Punkte."""
    width = max(0.9, 0.035 * pitch)
    lines = origin + np.arange(size, dtype=np.float32) * pitch

    dx = np.abs(xx[:, :, None] - lines[None, None, :]).min(axis=2)
    dy = np.abs(yy[:, :, None] - lines[None, None, :]).min(axis=2)

    inside_x = (xx >= lines[0] - width) & (xx <= lines[-1] + width)
    inside_y = (yy >= lines[0] - width) & (yy <= lines[-1] + width)

    cover = np.maximum(
        _soft_edge(dy, width) * inside_x,
        _soft_edge(dx, width) * inside_y,
    )

    for sx, sy in star_points(size):
        r = 0.095 * pitch
        d = np.hypot(xx - (origin + sx * pitch), yy - (origin + sy * pitch))
        cover = np.maximum(cover, _soft_edge(d, r * 2.0))

    ink = float(rng.uniform(0.55, 0.8))

    return img * (1.0 - ink * cover[:, :, None])


def _soft_edge(dist: np.ndarray, width: float) -> np.ndarray:
    """Antialiasing-Rampe: 1 innerhalb, 0 ausserhalb, 1 px Uebergang."""
    return np.clip(0.5 * width + 0.5 - dist, 0.0, 1.0)


def _shadows(img, labels, origin, pitch, light, xx, yy, rng):
    """Schlagschatten, entgegen der Lichtrichtung versetzt."""
    offset = -light[:2] * pitch * 0.22
    radius = 0.5 * pitch
    strength = float(rng.uniform(0.25, 0.5))
    out = img

    for y, x in zip(*np.nonzero(labels != LABEL_EMPTY)):
        cx = origin + x * pitch + offset[0]
        cy = origin + y * pitch + offset[1]
        box = _box(xx.shape[0], cx, cy, radius * 1.8)

        if box is None:
            continue

        y0, y1, x0, x1 = box
        d = np.hypot(xx[y0:y1, x0:x1] - cx, yy[y0:y1, x0:x1] - cy)
        soft = np.clip(1.0 - (d / (radius * 1.35)) ** 2, 0.0, 1.0)
        out[y0:y1, x0:x1] *= (1.0 - strength * soft)[:, :, None]

    return out


def _stones(img, labels, origin, pitch, light, xx, yy, rng):
    """Steine als schattierte Kugeln mit Glanzlicht."""
    radius = float(rng.uniform(0.45, 0.485)) * pitch
    shine = float(rng.uniform(0.35, 0.9))
    out = img

    for y, x in zip(*np.nonzero(labels != LABEL_EMPTY)):
        cx, cy = origin + x * pitch, origin + y * pitch
        box = _box(xx.shape[0], cx, cy, radius + 2)

        if box is None:
            continue

        y0, y1, x0, x1 = box
        nx = (xx[y0:y1, x0:x1] - cx) / radius
        ny = (yy[y0:y1, x0:x1] - cy) / radius
        rr = nx * nx + ny * ny
        inside = rr <= 1.0

        if not inside.any():
            continue

        nz = np.sqrt(np.clip(1.0 - rr, 0.0, 1.0))
        diffuse = np.clip(nx * light[0] + ny * light[1] + nz * light[2], 0.0, 1.0)

        # Halbvektor bei Blickrichtung (0,0,1) — Phong ohne Kamerabewegung.
        half = light + np.array([0.0, 0.0, 1.0], dtype=np.float32)
        half /= np.linalg.norm(half)
        spec = np.clip(nx * half[0] + ny * half[1] + nz * half[2], 0.0, 1.0)

        if labels[y, x] == LABEL_BLACK:
            base = np.float32(rng.uniform(16, 34))
            spec_power, spec_gain = 48.0, 210.0 * shine
        else:
            base = np.float32(rng.uniform(228, 248))
            spec_power, spec_gain = 26.0, 90.0 * shine

        shaded = base * (0.32 + 0.68 * diffuse) + spec_gain * spec**spec_power
        shaded = np.clip(shaded, 0, 255)

        # Weicher Rand: 1 px Alpha-Rampe statt harter Kante.
        alpha = np.clip((1.0 - np.sqrt(rr)) * radius, 0.0, 1.0)
        patch = out[y0:y1, x0:x1]
        out[y0:y1, x0:x1] = patch * (1 - alpha[:, :, None]) + shaded[:, :, None] * alpha[
            :, :, None
        ]

    return out


def _box(side: int, cx: float, cy: float, r: float):
    """Ganzzahliger Ausschnitt um (cx, cy) mit Radius r, auf das Bild geklemmt."""
    x0, x1 = int(np.floor(cx - r)), int(np.ceil(cx + r)) + 1
    y0, y1 = int(np.floor(cy - r)), int(np.ceil(cy + r)) + 1
    x0, y0 = max(0, x0), max(0, y0)
    x1, y1 = min(side, x1), min(side, y1)

    if x1 <= x0 or y1 <= y0:
        return None

    return y0, y1, x0, x1


def _lighting(rng, img, xx, yy, side):
    """Globaler Helligkeitsverlauf plus Vignette."""
    angle = float(rng.uniform(0, 2 * np.pi))
    strength = float(rng.uniform(0.05, 0.24))
    ramp = ((xx * np.cos(angle) + yy * np.sin(angle)) / side)
    ramp = (ramp - ramp.min()) / (np.ptp(ramp) + 1e-6)

    d = np.hypot(xx - side / 2, yy - side / 2) / (side / 2 * np.sqrt(2))
    vignette = 1.0 - float(rng.uniform(0.05, 0.3)) * d**2

    gain = (1.0 - strength / 2 + strength * ramp) * vignette

    return img * gain[:, :, None]
