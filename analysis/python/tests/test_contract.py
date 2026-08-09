"""Vertrag, Merkmale und Fensterung — alles ohne Modellgewichte prüfbar."""

from __future__ import annotations

import json

import numpy as np
import pytest

from goteach_salience.contract import Game, Window, to_gtp, windows_to_json
from goteach_salience.features import (
    BASE_CHANNELS,
    HISTORY,
    INPUT_CHANNELS,
    build_input,
    target,
    usable_turns,
)
from goteach_salience.train import synthetic_game
from goteach_salience.windows import find_windows, observed_salience


def sample_payload(size: int = 5, turns: int = 6) -> str:
    rng = np.random.default_rng(3)
    entries = []

    for t in range(turns):
        rows = ["." * size for _ in range(size)]
        rows[t % size] = "X" + "." * (size - 1)
        entries.append(
            {
                "rows": rows,
                "ownership": rng.uniform(-1, 1, size=size * size).tolist(),
            }
        )

    return json.dumps({"size": size, "turns": entries})


def test_vertrag_wird_gelesen():
    game = Game.from_json(sample_payload())

    assert game.size == 5
    assert len(game.turns) == 6
    assert game.turns[0].ownership.shape == (5, 5)


def test_steine_werden_in_zwei_ebenen_getrennt():
    game = Game.from_json(sample_payload())
    black, white = game.turns[0].stones()

    assert black.shape == (5, 5)
    assert white.shape == (5, 5)
    assert black.sum() == 1.0
    assert white.sum() == 0.0


@pytest.mark.parametrize(
    "payload",
    [
        json.dumps({"size": 5, "turns": [{"rows": ["..."], "ownership": [0] * 25}]}),
        json.dumps({"size": 2, "turns": [{"rows": ["..", ".."], "ownership": [0]}]}),
    ],
)
def test_ungueltige_vertraege_werden_abgelehnt(payload):
    with pytest.raises(ValueError):
        Game.from_json(payload)


def test_zu_kurze_partie_wird_abgelehnt():
    payload = json.dumps(
        {"size": 2, "turns": [{"rows": ["..", ".."], "ownership": [0, 0, 0, 0]}]}
    )

    with pytest.raises(ValueError):
        Game.from_json(payload)


def test_gtp_koordinaten_ueberspringen_das_i():
    # Spalte 8 ist J, nicht I — dieselbe Konvention wie board/coords.go.
    assert to_gtp(0, 0, 19) == "A19"
    assert to_gtp(8, 0, 19) == "J19"
    assert to_gtp(18, 18, 19) == "T1"


def test_eingangstensor_traegt_die_historie():
    game = synthetic_game(1, size=9, turns=20)
    tensor = build_input(game, 10)

    assert tensor.shape == (INPUT_CHANNELS, 9, 9)
    assert INPUT_CHANNELS == HISTORY * BASE_CHANNELS
    assert tensor.dtype == np.float32


def test_eingangstensor_am_partiestart_wiederholt_statt_zu_nullen():
    game = synthetic_game(2, size=9, turns=20)
    tensor = build_input(game, 0)

    # Alle vier Zeitschritte zeigen dieselbe Stellung; ein Nullblock wäre eine
    # Aussage über ein leeres Brett und damit eine andere Stellung.
    first = tensor[:BASE_CHANNELS]
    second = tensor[BASE_CHANNELS : 2 * BASE_CHANNELS]

    assert np.allclose(first, second)


def test_lernziel_misst_die_zukunft():
    game = synthetic_game(3, size=9, turns=40)
    early = target(game, 5)

    assert early.shape == (9, 9)
    assert early.min() >= 0.0
    assert early.max() <= 1.0 + 1e-6

    # Am Partieende gibt es keine Zukunft mehr, also auch nichts zu lernen.
    assert target(game, len(game.turns) - 1).max() == 0.0


def test_nutzbare_zuege_lassen_platz_fuer_den_horizont():
    game = synthetic_game(4, size=9, turns=40)
    turns = usable_turns(game)

    assert turns.start == 0
    assert turns.stop < len(game.turns)


def test_fenster_entstehen_und_sind_sortiert():
    game = synthetic_game(5, size=13, turns=60)
    windows = find_windows(game, observed_salience(game), top=5)

    assert windows
    assert all(w.to_turn >= w.from_turn for w in windows)
    assert all(w.points for w in windows)

    scores = [w.score for w in windows]
    assert scores == sorted(scores, reverse=True)
    assert scores[0] == pytest.approx(1.0)


def test_fensterung_ist_reproduzierbar():
    game = synthetic_game(6, size=13, turns=60)
    salience = observed_salience(game)

    first = find_windows(game, salience)
    second = find_windows(game, salience)

    assert [w.to_dict() for w in first] == [w.to_dict() for w in second]


def test_ruhiges_feld_ergibt_keine_fenster():
    game = synthetic_game(7, size=9, turns=20)
    flat = np.zeros((len(game.turns), 9, 9), dtype=np.float32)

    assert find_windows(game, flat) == []


def test_fenster_json_haelt_den_vertrag():
    payload = json.loads(
        windows_to_json([Window(from_turn=3, to_turn=9, points=["D4"], score=0.5)])
    )

    assert payload["windows"][0]["fromTurn"] == 3
    assert payload["windows"][0]["toTurn"] == 9
    assert payload["windows"][0]["points"] == ["D4"]
