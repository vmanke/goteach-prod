"""Kanonisches Gitter und die preinformed-Kanaele."""

from __future__ import annotations

import numpy as np
import pytest

from goteach_vision.contract import BOARD_SIZES
from goteach_vision.grid import (
    DEFAULT_MARGIN,
    cell_centres,
    intersections,
    line_positions,
    pitch,
    preinformed_channels,
    star_points,
)


@pytest.mark.parametrize("size", BOARD_SIZES)
def test_linien_sind_gleichmaessig(size):
    side = 512
    positions = line_positions(size, side)

    assert len(positions) == size
    assert positions[0] == pytest.approx(side * DEFAULT_MARGIN)
    assert positions[-1] == pytest.approx(side * (1 - DEFAULT_MARGIN))
    assert np.allclose(np.diff(positions), pitch(size, side))


@pytest.mark.parametrize("size", BOARD_SIZES)
def test_schnittpunkte_sind_row_major(size):
    points = intersections(size, 512).reshape(size, size, 2)

    # Innerhalb einer Zeile aendert sich x, y bleibt gleich.
    assert np.allclose(points[0, :, 1], points[0, 0, 1])
    assert points[0, 1, 0] > points[0, 0, 0]

    # Zeile 0 liegt oben.
    assert points[1, 0, 1] > points[0, 0, 1]


@pytest.mark.parametrize("size", BOARD_SIZES)
def test_preinformed_kanaele(size):
    side = 256
    channels = preinformed_channels(size, side)

    assert channels.shape == (3, side, side)
    assert channels.dtype == np.float32

    mask, dx, dy = channels

    # Die Maske ist auf den Linien maximal und in den Zellmitten minimal.
    positions = line_positions(size, side)
    centre = int(round(positions[len(positions) // 2]))
    middle = int(round((positions[0] + positions[1]) / 2))

    # Auf der Linie ausgewertet: der naechste Pixel kann bis zu einen halben
    # daneben liegen, deshalb das kleine Fenster.
    assert mask[centre - 1 : centre + 2, centre - 1 : centre + 2].max() > 0.85
    assert mask[middle, middle] < 0.2

    # Die Abstandskanaele liegen im Brettinneren in [-0.5, 0.5].
    inner = slice(int(positions[0]), int(positions[-1]))
    assert np.abs(dx[inner, inner]).max() <= 0.51
    assert np.abs(dy[inner, inner]).max() <= 0.51


@pytest.mark.parametrize("size", BOARD_SIZES)
def test_zellmitten_liegen_zwischen_den_linien(size):
    side = 512
    centres = cell_centres(size, side)
    positions = line_positions(size, side)

    assert len(centres) == (size - 1) ** 2

    distance = np.abs(centres[:, 0][:, None] - positions[None, :]).min(axis=1)

    # Eine Zellmitte ist eine halbe Gitterteilung von jeder Linie entfernt.
    assert np.allclose(distance, pitch(size, side) / 2)


def test_hoshi_punkte():
    assert (3, 3) in star_points(19)
    assert (9, 9) in star_points(19)
    assert len(star_points(19)) == 9
    assert (2, 2) in star_points(9)
    assert (3, 3) in star_points(13)
