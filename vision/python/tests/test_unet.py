"""U-Net, Training und ONNX-Export.

Alles hier braucht torch und wird ohne torch uebersprungen — die Basis der
Pipeline (Geometrie, klassisches Backend, Vertrag) muss ohne ML testbar
bleiben.
"""

from __future__ import annotations

import numpy as np
import pytest

from goteach_vision.features import INPUT_CHANNELS, build_input, target_mask
from goteach_vision.grid import DEFAULT_MARGIN
from goteach_vision.render.screenshot import render_screenshot

torch = pytest.importorskip("torch", reason="torch nur fuer Training/Export noetig")

pytestmark = pytest.mark.torch


def test_eingangstensor_hat_die_vereinbarte_form():
    sample = render_screenshot(np.random.default_rng(1), 19, palette=0)
    warped = np.zeros((128, 128, 3), dtype=np.uint8)

    tensor = build_input(warped, 19, DEFAULT_MARGIN)

    assert tensor.shape == (INPUT_CHANNELS, 128, 128)
    assert tensor.dtype == np.float32
    # Farbkanaele zentriert, Geometriekanaele unabhaengig vom Bild.
    assert tensor[:3].min() == pytest.approx(-0.5)
    assert sample.size == 19


def test_zielmaske_deckt_die_steine():
    labels = np.zeros((9, 9), dtype=np.int8)
    labels[4, 4] = 1

    mask = target_mask(labels, 9, 128, DEFAULT_MARGIN)

    assert mask.shape == (128, 128)
    assert set(np.unique(mask)) == {0, 1}
    # Ein Stein bedeckt spuerbar Flaeche, aber laengst nicht das Brett.
    assert 0.0 < mask.mean() < 0.05


def test_netz_erhaelt_die_aufloesung():
    from goteach_vision.unet import NUM_CLASSES, UNet, parameter_count

    model = UNet()
    output = model(torch.zeros(1, INPUT_CHANNELS, 128, 128))

    assert output.shape == (1, NUM_CLASSES, 128, 128)
    # Klein genug fuers Training auf bescheidener Hardware.
    assert parameter_count(model) < 5_000_000


def test_export_stimmt_mit_torch_ueberein(tmp_path):
    pytest.importorskip("onnxruntime")
    pytest.importorskip("onnxscript")

    from goteach_vision.export import export
    from goteach_vision.unet import UNet

    path = tmp_path / "net.onnx"

    # export() prueft selbst gegen torch und wirft bei Abweichung.
    export(UNet(), str(path), side=64, tolerance=1e-3)

    assert path.exists()


def test_training_laeuft_und_senkt_den_verlust():
    from goteach_vision.train import train

    model = train(steps=3, batch=1, side=96, validate_every=0)

    assert model is not None
