"""PNG einlesen und auf einen definierten RGB-Tensor normalisieren.

PNG kennt Alpha, Palettenfarben, Graustufen und 16 Bit pro Kanal. Keine
dieser Eigenheiten darf bis ins Netz durchschlagen — deshalb liegt die
Normalisierung hier am Rand der Pipeline und liefert immer dasselbe:
ein ``(H, W, 3)``-Array aus ``uint8``.
"""

from __future__ import annotations

import io
import sys
from pathlib import Path

import numpy as np
from PIL import Image

# Alpha wird gegen Weiss komponiert: Diagramme mit transparentem Hintergrund
# sind fast immer fuer helle Darstellung gedacht.
_BACKDROP = (255, 255, 255)

# Modi, die PIL nicht direkt nach RGBA wandeln kann; sie brauchen den Umweg
# ueber ein explizit auf 8 Bit gebrachtes Array.
_WIDE_MODES = ("I", "I;16", "I;16B", "I;16L", "I;16N", "F")


def load_rgb(src) -> np.ndarray:
    """Laedt ein PNG aus Pfad, Bytes, Dateiobjekt oder '-' (stdin)."""
    if isinstance(src, (bytes, bytearray, memoryview)):
        img = Image.open(io.BytesIO(bytes(src)))

    elif isinstance(src, (str, Path)):
        if str(src) == "-":
            img = Image.open(io.BytesIO(sys.stdin.buffer.read()))
        else:
            img = Image.open(src)

    else:
        img = Image.open(src)

    with img:
        return normalize_to_rgb(img)


def normalize_to_rgb(img: Image.Image) -> np.ndarray:
    """Bringt ein beliebiges PIL-Bild auf (H, W, 3) uint8."""
    img = _to_eight_bit(img)

    # Transparenz kann als Modus (RGBA/LA/PA) oder — bei Palettenbildern —
    # als tRNS-Chunk in img.info stecken; beide Wege muessen komponiert werden.
    if img.mode in ("RGBA", "LA", "PA") or "transparency" in img.info:
        rgba = img.convert("RGBA")
        backdrop = Image.new("RGBA", rgba.size, _BACKDROP + (255,))
        img = Image.alpha_composite(backdrop, rgba)

    return np.asarray(img.convert("RGB"), dtype=np.uint8)


def _to_eight_bit(img: Image.Image) -> Image.Image:
    """Reduziert 16-Bit- und Float-Modi auf 8 Bit Graustufen."""
    if img.mode not in _WIDE_MODES:
        return img

    arr = np.asarray(img)

    if np.issubdtype(arr.dtype, np.floating):
        # Float-PNGs gibt es praktisch nicht, TIFF-artige Zwischenstufen aber
        # schon; Konvention: Wertebereich [0, 1].
        arr = np.clip(arr * 255.0, 0, 255).astype(np.uint8)

    elif arr.size and arr.max() > 255:
        # Echte 16-Bit-Daten: obere 8 Bit behalten (entspricht /257 bis auf
        # ein halbes LSB und bleibt exakt bei 0 und 65535).
        arr = (arr >> 8).astype(np.uint8)

    else:
        arr = np.clip(arr, 0, 255).astype(np.uint8)

    return Image.fromarray(arr, mode="L")


def save_rgb(arr: np.ndarray, path) -> None:
    """Schreibt ein (H, W, 3)-uint8-Array als PNG."""
    Image.fromarray(np.asarray(arr, dtype=np.uint8), mode="RGB").save(path, format="PNG")
