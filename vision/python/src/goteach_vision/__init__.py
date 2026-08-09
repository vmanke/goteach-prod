"""goteach-vision — Stufen 1-2 der Go-Analysearchitektur.

Aus einem PNG eines Go-Bretts wird eine symbolische Stellung im
Austauschformat der Go-Seite (siehe ``vision/adapter.go`` im
Wurzelverzeichnis dieses Repositorys).
"""

from .contract import BOARD_SIZES, LABEL_BLACK, LABEL_EMPTY, LABEL_WHITE, Position

__all__ = [
    "Position",
    "BOARD_SIZES",
    "LABEL_EMPTY",
    "LABEL_BLACK",
    "LABEL_WHITE",
    "__version__",
]

__version__ = "0.1.0"
