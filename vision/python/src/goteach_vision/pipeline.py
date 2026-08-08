"""Die Pipeline von der PNG-Datei zur symbolischen Stellung.

Ein Einstiegspunkt fuer beide Bilddomaenen: :func:`detect_position` nimmt
Screenshots wie Fotos entgegen, weil die Entzerrung in :mod:`.geometry` bei
achsparallelen Vorlagen von selbst zur reinen Skalierung degeneriert.
"""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np

from . import classical, geometry
from .contract import Position
from .grid import DEFAULT_MARGIN


@dataclass
class Detection:
    """Erkannte Stellung samt Diagnose (letztere gehoert auf stderr)."""

    position: Position
    confidence: np.ndarray  # (size, size) in [0, 1]
    geometry: geometry.Geometry
    backend: str

    @property
    def min_confidence(self) -> float:
        return float(self.confidence.min())

    @property
    def mean_confidence(self) -> float:
        return float(self.confidence.mean())


def detect_position(
    rgb: np.ndarray,
    size_hint: int | None = None,
    komi: float | None = None,
    backend: str = "auto",
    weights: str | None = None,
    margin: float = DEFAULT_MARGIN,
) -> Detection:
    """Erkennt Brett und Steine in einem RGB-Bild."""
    geo = geometry.detect(rgb, size_hint=size_hint, margin=margin)
    chosen = _resolve_backend(backend, weights)

    if chosen == "onnx":
        from . import infer

        labels, confidence = infer.classify(geo.warped, geo.size, weights, margin)
    else:
        labels, confidence = classical.classify(geo.warped, geo.size, margin)

    return Detection(
        position=Position.from_labels(labels, komi=komi),
        confidence=confidence,
        geometry=geo,
        backend=chosen,
    )


def _resolve_backend(backend: str, weights: str | None) -> str:
    """Waehlt das Backend; 'auto' nimmt das Netz, sobald Gewichte da sind."""
    if backend == "auto":
        return "onnx" if weights else "classical"

    if backend == "onnx" and not weights:
        raise ValueError("Backend 'onnx' verlangt --weights")

    if backend not in ("onnx", "classical"):
        raise ValueError(f"unbekanntes Backend {backend!r}")

    return backend
