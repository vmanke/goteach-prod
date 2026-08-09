"""Der JSON-Vertrag muss exakt dem entsprechen, was vision/adapter.go liest."""

from __future__ import annotations

import json

import numpy as np
import pytest

from goteach_vision.contract import (
    LABEL_BLACK,
    LABEL_EMPTY,
    LABEL_WHITE,
    Position,
)


def test_zeichen_und_reihenfolge():
    labels = np.array(
        [
            [LABEL_EMPTY, LABEL_BLACK, LABEL_EMPTY],
            [LABEL_EMPTY, LABEL_EMPTY, LABEL_WHITE],
            [LABEL_EMPTY, LABEL_EMPTY, LABEL_EMPTY],
        ],
        dtype=np.int8,
    )

    position = Position.from_labels(labels)

    # rows[0] ist die oberste Brettzeile, '.' leer, 'X' schwarz, 'O' weiss.
    assert position.rows == (".X.", "..O", "...")
    assert position.size == 3


def test_json_felder():
    position = Position.from_labels(np.zeros((9, 9), dtype=np.int8), komi=7.5)
    payload = json.loads(position.to_json())

    assert payload["size"] == 9
    assert len(payload["rows"]) == 9
    assert all(len(row) == 9 for row in payload["rows"])
    assert payload["komi"] == 7.5


def test_komi_wird_weggelassen_wenn_unbekannt():
    position = Position.from_labels(np.zeros((9, 9), dtype=np.int8))

    # Go liest komi als "omitempty"; ein fehlendes Feld ist der saubere Weg,
    # "nicht bekannt" auszudruecken statt einer erfundenen Null.
    assert "komi" not in json.loads(position.to_json())


def test_labels_roundtrip():
    rng = np.random.default_rng(4)
    labels = rng.integers(0, 3, size=(19, 19)).astype(np.int8)

    assert np.array_equal(Position.from_labels(labels).to_labels(), labels)


def test_steine_zaehlen():
    labels = np.zeros((9, 9), dtype=np.int8)
    labels[0, 0] = LABEL_BLACK
    labels[1, 1] = LABEL_WHITE
    labels[2, 2] = LABEL_WHITE

    assert Position.from_labels(labels).stones() == 3


@pytest.mark.parametrize(
    "labels",
    [
        np.zeros((3, 4), dtype=np.int8),
        np.full((3, 3), 7, dtype=np.int8),
    ],
)
def test_ungueltige_eingaben(labels):
    with pytest.raises(ValueError):
        Position.from_labels(labels)
