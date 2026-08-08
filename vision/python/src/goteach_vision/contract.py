"""Austauschformat zur Go-Seite.

Der Vertrag ist in ``vision/adapter.go`` definiert und bewusst schmal:
``size``, ``rows`` und optional ``komi``. ``rows[0]`` ist die oberste
Brettzeile; '.' leer, 'X' schwarz, 'O' weiss.

Alles, was die Erkennung sonst noch weiss — Konfidenz, Domaene, Laufzeit —
gehoert auf stderr und niemals in dieses JSON: Go parst es strikt und wuerde
Zusatzfelder zwar ignorieren, aber der schmale Vertrag ist genau der Punkt
der Bruecke.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

import numpy as np

# Label-Kodierung der internen Arrays (Renderer, Netz, Nachverarbeitung).
LABEL_EMPTY = 0
LABEL_BLACK = 1
LABEL_WHITE = 2

# Zeichen des JSON-Vertrags, indiziert mit den LABEL_*-Konstanten.
SYMBOLS = ".XO"

# Brettgroessen, die goteach unterstuetzt (quadratisch, siehe README).
BOARD_SIZES = (9, 13, 19)


@dataclass(frozen=True)
class Position:
    """Symbolische Stellung, wie die Go-Seite sie erwartet."""

    size: int
    rows: tuple[str, ...]
    komi: float | None = None

    @classmethod
    def from_labels(cls, labels: np.ndarray, komi: float | None = None) -> "Position":
        """Baut die Stellung aus einem (size, size)-Array mit LABEL_*-Werten.

        Zeile 0 des Arrays ist die oberste Brettzeile — dieselbe Ordnung, die
        KataGo fuer Ownership benutzt (row-major ab oben links).
        """
        arr = np.asarray(labels)

        if arr.ndim != 2 or arr.shape[0] != arr.shape[1]:
            raise ValueError(f"quadratisches Label-Array erwartet, {arr.shape} erhalten")

        if arr.min() < 0 or arr.max() >= len(SYMBOLS):
            raise ValueError("Label ausserhalb von LABEL_EMPTY/BLACK/WHITE")

        table = np.array(list(SYMBOLS))
        rows = tuple("".join(table[row]) for row in arr)

        return cls(size=int(arr.shape[0]), rows=rows, komi=komi)

    def to_dict(self) -> dict:
        out: dict = {"size": self.size, "rows": list(self.rows)}

        # komi ist im Go-Vertrag "omitempty" — nur mitschicken, wenn bekannt.
        if self.komi is not None:
            out["komi"] = self.komi

        return out

    def to_json(self) -> str:
        return json.dumps(self.to_dict(), ensure_ascii=False)

    def to_labels(self) -> np.ndarray:
        """Umkehrung von :meth:`from_labels` (fuer Tests und den Renderer)."""
        lookup = {ch: i for i, ch in enumerate(SYMBOLS)}

        return np.array(
            [[lookup[ch] for ch in row] for row in self.rows], dtype=np.int8
        )

    def stones(self) -> int:
        return sum(row.count("X") + row.count("O") for row in self.rows)
