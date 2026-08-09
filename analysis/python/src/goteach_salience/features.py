"""Eingangstensor und Lernziel — eine einzige Quelle der Wahrheit.

Training, ONNX-Export und Inferenz muessen denselben Tensor bauen; jede
Abweichung waere ein stiller Genauigkeitsverlust, den kein Test bemerkt.
"""

from __future__ import annotations

import numpy as np

from .contract import Game

# So viele Zuege Vergangenheit gehen in den Eingang. Das ist das "2D mit
# Historie": Der Stapel traegt nicht nur die Stellung, sondern ihren Verlauf.
HISTORY = 4

# Ebenen je Zeitschritt: schwarz, weiss, Ownership, Ownership-Aenderung.
BASE_CHANNELS = 4

INPUT_CHANNELS = HISTORY * BASE_CHANNELS

# Horizont des Lernziels: ueber so viele Zuege in die Zukunft wird die
# Umwaelzung gemessen, die vorhergesagt werden soll.
HORIZON = 12


def frame(game: Game, turn: int) -> np.ndarray:
    """Die vier Ebenen eines einzelnen Zeitschritts."""
    size = game.size
    index = max(0, min(turn, len(game.turns) - 1))
    current = game.turns[index]
    black, white = current.stones()

    previous = game.turns[max(0, index - 1)]
    delta = np.abs(current.ownership - previous.ownership)

    return np.stack([black, white, current.ownership, delta], axis=0).astype(
        np.float32, copy=False
    ).reshape(BASE_CHANNELS, size, size)


def build_input(game: Game, turn: int) -> np.ndarray:
    """Baut (INPUT_CHANNELS, size, size) fuer einen Zug samt Historie.

    Vor dem Partiestart wird der aelteste verfuegbare Zeitschritt wiederholt,
    statt mit Nullen aufzufuellen: Ein leeres Brett ist eine Aussage ueber die
    Stellung, kein Platzhalter, und das Netz soll den Unterschied nicht lernen
    muessen.
    """
    frames = [frame(game, turn - offset) for offset in range(HISTORY)]

    return np.concatenate(frames, axis=0)


def target(game: Game, turn: int, horizon: int = HORIZON) -> np.ndarray:
    """Selbstueberwachtes Lernziel: die kuenftige Umwaelzung je Punkt.

    Gemessen wird, wie stark sich die Zugehoerigkeit eines Punktes in den
    naechsten ``horizon`` Zuegen noch veraendert. Vorhergesagt werden soll das
    aus der *jetzigen* Stellung — das Netz lernt also, Brisanz zu erkennen,
    bevor sie sich auszahlt.

    Damit braucht es keine Labels. Das ist der Punkt: "narrativ interessant"
    hat keine Ground Truth, "hier wird sich noch etwas entscheiden" schon.
    """
    size = game.size
    total = np.zeros((size, size), dtype=np.float32)
    last = min(turn + horizon, len(game.turns) - 1)

    for t in range(turn + 1, last + 1):
        total += np.abs(game.turns[t].ownership - game.turns[t - 1].ownership)

    # Auf [0, 1] normieren, damit der Verlust ueber Partien hinweg vergleichbar
    # bleibt; eine ruhige Partie soll nicht einfach kleinere Gradienten geben.
    peak = float(total.max())

    if peak > 1e-6:
        total /= peak

    return total


def usable_turns(game: Game, horizon: int = HORIZON) -> range:
    """Zuege, fuer die es sowohl Historie als auch Zukunft gibt."""
    return range(0, max(1, len(game.turns) - horizon))
