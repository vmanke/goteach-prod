"""PNG-Eigenheiten duerfen nicht bis ins Netz durchschlagen."""

from __future__ import annotations

import numpy as np
import pytest
from PIL import Image

from goteach_vision.pngio import load_rgb


def _write(tmp_path, image, name):
    path = tmp_path / name
    image.save(path, format="PNG")

    return path


def test_rgb_bleibt_unveraendert(tmp_path):
    data = np.array([[[10, 20, 30], [40, 50, 60]]], dtype=np.uint8)
    path = _write(tmp_path, Image.fromarray(data, "RGB"), "rgb.png")

    assert np.array_equal(load_rgb(path), data)


def test_alpha_wird_gegen_weiss_komponiert(tmp_path):
    # Halbtransparentes Schwarz auf Weiss ergibt Mittelgrau.
    data = np.array([[[0, 0, 0, 128], [0, 0, 0, 255]]], dtype=np.uint8)
    path = _write(tmp_path, Image.fromarray(data, "RGBA"), "alpha.png")

    out = load_rgb(path)

    assert out.shape == (1, 2, 3)
    assert out[0, 1].tolist() == [0, 0, 0]
    assert 120 <= int(out[0, 0][0]) <= 135


def test_palette_wird_aufgeloest(tmp_path):
    source = Image.fromarray(
        np.array([[[200, 10, 10], [10, 200, 10]]], dtype=np.uint8), "RGB"
    )
    path = _write(tmp_path, source.convert("P", palette=Image.ADAPTIVE), "palette.png")

    out = load_rgb(path)

    assert out.shape == (1, 2, 3)
    assert out[0, 0][0] > out[0, 0][1]
    assert out[0, 1][1] > out[0, 1][0]


def test_graustufen_wird_zu_rgb(tmp_path):
    data = np.array([[0, 128, 255]], dtype=np.uint8)
    path = _write(tmp_path, Image.fromarray(data, "L"), "gray.png")

    out = load_rgb(path)

    assert out.shape == (1, 3, 3)
    assert out[0, 1].tolist() == [128, 128, 128]


def test_sechzehn_bit_wird_auf_acht_reduziert(tmp_path):
    data = np.array([[0, 32768, 65535]], dtype=np.uint16)
    path = _write(tmp_path, Image.fromarray(data, "I;16"), "wide.png")

    out = load_rgb(path)

    assert out.dtype == np.uint8
    assert out.shape == (1, 3, 3)
    assert out[0, 0][0] == 0
    assert out[0, 2][0] == 255
    assert 120 <= int(out[0, 1][0]) <= 135


def test_bytes_und_pfad_liefern_dasselbe(tmp_path):
    data = np.array([[[1, 2, 3], [4, 5, 6]]], dtype=np.uint8)
    path = _write(tmp_path, Image.fromarray(data, "RGB"), "both.png")

    assert np.array_equal(load_rgb(path), load_rgb(path.read_bytes()))


def test_kaputte_datei_meldet_fehler(tmp_path):
    path = tmp_path / "broken.png"
    path.write_bytes(b"nicht wirklich ein PNG")

    with pytest.raises(Exception):
        load_rgb(path)
