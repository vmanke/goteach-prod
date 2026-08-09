"""Inferenz des Salienzmoduls.

Ohne Gewichte laeuft der beobachtete Pfad, mit Gewichten das Netz ueber die
ONNX Runtime — torch wird zur Laufzeit nicht gebraucht.
"""

from __future__ import annotations

import numpy as np

from .contract import Game
from .features import build_input, usable_turns
from .windows import observed_salience

_SESSIONS: dict[str, object] = {}


def session(weights: str):
    if weights not in _SESSIONS:
        import onnxruntime

        _SESSIONS[weights] = onnxruntime.InferenceSession(
            weights, providers=["CPUExecutionProvider"]
        )

    return _SESSIONS[weights]


def predicted_salience(game: Game, weights: str) -> np.ndarray:
    """Vorhergesagte Brisanz je Zug und Punkt.

    Die Vorhersage laeuft nur ueber Zuege, fuer die es Historie *und* Zukunft
    gibt; ausserhalb bleibt das Feld bei null. Dort etwas zu erfinden waere
    das Gegenteil dessen, was das Modul leisten soll.
    """
    run = session(weights)
    name = run.get_inputs()[0].name

    out = np.zeros((len(game.turns), game.size, game.size), dtype=np.float32)

    for turn in usable_turns(game):
        tensor = build_input(game, turn)[None]
        prediction = np.asarray(run.run(None, {name: tensor})[0])
        out[turn] = np.clip(prediction[0, 0], 0.0, None)

    return out


def salience(game: Game, weights: str | None = None) -> np.ndarray:
    if weights:
        return predicted_salience(game, weights)

    return observed_salience(game)
