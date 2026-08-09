"""Austauschformat zur Go-Seite.

Hinein geht der Verlauf einer Partie — je Zug die Stellung und das
Ownership-Feld, das KataGo ohnehin liefert. Heraus kommen Fenster: Bereiche
aus Zugspanne und Brettpunkten, nach Brisanz sortiert.

Die Go-Seite entscheidet danach weiter (Formen benennen, Zuege zuordnen,
Zahlen nachrechnen); dieses Modul waehlt nur aus. Das ist die Arbeitsteilung,
die die Halluzinationssperre traegt: Ein gelerntes Modell darf bestimmen,
*worueber* geredet wird, niemals *was* dabei behauptet wird.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

import numpy as np

# Zeichen der Stellungszeilen, identisch zum Vertrag in vision/adapter.go.
EMPTY, BLACK, WHITE = ".", "X", "O"


@dataclass(frozen=True)
class Turn:
    """Eine Stellung samt Ownership-Feld."""

    rows: tuple[str, ...]
    ownership: np.ndarray

    def stones(self) -> tuple[np.ndarray, np.ndarray]:
        """Zwei Belegungsebenen: schwarz und weiss, je (size, size) float32."""
        size = len(self.rows)
        black = np.zeros((size, size), dtype=np.float32)
        white = np.zeros((size, size), dtype=np.float32)

        for y, row in enumerate(self.rows):
            for x, cell in enumerate(row):
                if cell in ("X", "B"):
                    black[y, x] = 1.0
                elif cell in ("O", "W"):
                    white[y, x] = 1.0

        return black, white


@dataclass(frozen=True)
class Game:
    """Der Verlauf einer Partie, wie ihn die Go-Seite uebergibt."""

    size: int
    turns: tuple[Turn, ...]

    @classmethod
    def from_json(cls, data: str | bytes) -> "Game":
        payload = json.loads(data)
        size = int(payload["size"])
        turns = []

        for entry in payload["turns"]:
            rows = tuple(entry["rows"])

            if len(rows) != size:
                raise ValueError(f"{len(rows)} Zeilen, {size} erwartet")

            ownership = np.asarray(entry["ownership"], dtype=np.float32)

            if ownership.size != size * size:
                raise ValueError(
                    f"Ownership-Laenge {ownership.size}, {size * size} erwartet"
                )

            turns.append(Turn(rows=rows, ownership=ownership.reshape(size, size)))

        if len(turns) < 2:
            raise ValueError("mindestens zwei Stellungen noetig")

        return cls(size=size, turns=tuple(turns))


@dataclass
class Window:
    """Ein Fenster ueber Zugspanne und Brettpunkte."""

    from_turn: int
    to_turn: int
    points: list[str]
    score: float

    def to_dict(self) -> dict:
        return {
            "fromTurn": self.from_turn,
            "toTurn": self.to_turn,
            "points": self.points,
            "score": round(float(self.score), 6),
        }


def windows_to_json(windows: list[Window]) -> str:
    return json.dumps({"windows": [w.to_dict() for w in windows]}, ensure_ascii=False)


# Spaltenbuchstaben ohne I — die GTP-Konvention, die auch board/coords.go nutzt.
_COLUMNS = "ABCDEFGHJKLMNOPQRSTUVWXYZ"


def to_gtp(x: int, y: int, size: int) -> str:
    """Punkt in GTP-Schreibweise; y = 0 ist die oberste Zeile."""
    return f"{_COLUMNS[x]}{size - y}"
