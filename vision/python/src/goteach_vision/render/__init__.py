"""Synthetischer Datengenerator fuer Stufen 1-2.

Es gibt keinen echten Datensatz; alles Trainingsmaterial entsteht hier.
Zwei Domaenen teilen sich einen Sampler: sauber gerenderte Diagramme
(``screenshot``) und simulierte Brettfotos (``photo``). Weil der Renderer
Stellung *und* Schnittpunktkoordinaten kennt, sind die Labels exakt.
"""

from __future__ import annotations

from dataclasses import dataclass

import numpy as np


@dataclass
class Sample:
    """Ein gerendertes Brett samt Ground Truth."""

    image: np.ndarray  # (H, W, 3) uint8
    labels: np.ndarray  # (size, size) int8 mit LABEL_*
    corners: np.ndarray  # (4, 2) float64: aeusserste Schnittpunkte TL,TR,BR,BL
    size: int
    domain: str
