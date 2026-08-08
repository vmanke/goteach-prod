"""Inferenz des U-Nets ueber die ONNX Runtime.

Zur Laufzeit ist torch nicht noetig — das ist der Grund fuer den Umweg ueber
ONNX. Das Modell wird einmal je Pfad geladen und danach wiederverwendet.
"""

from __future__ import annotations

import numpy as np

from .features import build_input
from .grid import DEFAULT_MARGIN
from .postprocess import decide, pool_logits

_SESSIONS: dict[str, object] = {}


def session(weights: str):
    """Laedt eine ONNX-Sitzung und haelt sie fuer weitere Aufrufe vor."""
    if weights not in _SESSIONS:
        import onnxruntime

        _SESSIONS[weights] = onnxruntime.InferenceSession(
            weights, providers=["CPUExecutionProvider"]
        )

    return _SESSIONS[weights]


def logits(warped: np.ndarray, size: int, weights: str, margin: float) -> np.ndarray:
    """Rohe Klassenlogits (3, side, side) fuer ein entzerrtes Bild."""
    run = session(weights)
    tensor = build_input(warped, size, margin)[None]
    name = run.get_inputs()[0].name

    return np.asarray(run.run(None, {name: tensor})[0])[0]


def classify(
    warped: np.ndarray, size: int, weights: str, margin: float = DEFAULT_MARGIN
) -> tuple[np.ndarray, np.ndarray]:
    """Klassifiziert alle Schnittpunkte mit dem Netz."""
    scores = pool_logits(logits(warped, size, weights, margin), size, margin)

    return decide(scores, size)
