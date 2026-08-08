"""Stufe 1: Entzerrung und Brettgroesse."""

from __future__ import annotations

import numpy as np
import pytest

from goteach_vision import geometry
from goteach_vision.contract import BOARD_SIZES
from goteach_vision.render.augment import apply_homography, perspective
from goteach_vision.render.screenshot import render_screenshot


def _pitch(sample) -> float:
    return float(abs(sample.corners[1, 0] - sample.corners[0, 0])) / (sample.size - 1)


@pytest.mark.parametrize("size", BOARD_SIZES)
def test_ecken_werden_wiedergefunden(size):
    sample = render_screenshot(np.random.default_rng(11), size, palette=0)
    result = geometry.detect(sample.image, size_hint=size)

    assert result.size == size

    # Ein Zehntel Gitterteilung ist deutlich genauer, als die Auslese
    # braucht (Scheibenradius 0.3, Steinradius 0.46).
    error = float(np.abs(result.corners - sample.corners).max())
    assert error < 0.1 * _pitch(sample)


def test_screenshot_entzerrung_degeneriert_zur_skalierung():
    """Bei achsparallelen Vorlagen darf keine Perspektive entstehen.

    Das ist die Eigenschaft, wegen der Screenshots und Fotos denselben
    Codepfad teilen koennen: die Homographie faellt hier von selbst auf eine
    reine Skalierung mit Versatz zusammen.
    """
    sample = render_screenshot(np.random.default_rng(5), 19, palette=0)
    matrix = geometry.detect(sample.image, size_hint=19).homography

    normalised = matrix / matrix[2, 2]
    scale = abs(normalised[0, 0])

    # Perspektivische Anteile: ueber die ganze Bildbreite duerfen sie den
    # Massstab um weniger als zwei Prozent veraendern.
    width = sample.image.shape[1]
    assert abs(normalised[2, 0]) * width < 0.02
    assert abs(normalised[2, 1]) * width < 0.02

    # Restdrehung/Scherung unter einem Grad.
    assert abs(normalised[0, 1]) / scale < 0.018
    assert abs(normalised[1, 0]) / scale < 0.018

    # Gleicher Massstab in beiden Achsen.
    assert normalised[0, 0] == pytest.approx(normalised[1, 1], rel=0.02)


def test_perspektive_wird_zurueckgerechnet():
    rng = np.random.default_rng(3)
    sample = render_screenshot(rng, 19, palette=0)
    warped = perspective(rng, sample, strength=0.05)

    result = geometry.detect(warped.image, size_hint=19)

    error = float(np.abs(result.corners - warped.corners).max())
    assert error < 0.35 * _pitch(sample)


def test_homographie_bildet_ecken_auf_das_kanonische_gitter_ab():
    sample = render_screenshot(np.random.default_rng(2), 13, palette=0)
    result = geometry.detect(sample.image, size_hint=13)

    mapped = apply_homography(result.homography, result.corners)
    positions = np.array(
        [result.side * result.margin, result.side * (1 - result.margin)]
    )

    assert mapped[0] == pytest.approx([positions[0], positions[0]], abs=1.0)
    assert mapped[2] == pytest.approx([positions[1], positions[1]], abs=1.0)


def test_ohne_brett_wird_abgebrochen():
    noise = (np.random.default_rng(1).random((200, 200, 3)) * 255).astype(np.uint8)

    with pytest.raises(geometry.BoardNotFound):
        geometry.detect(noise)
