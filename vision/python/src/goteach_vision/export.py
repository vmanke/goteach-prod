"""ONNX-Export samt Gegenprobe.

Der Export wird nicht blind geschrieben: Er wird sofort mit der ONNX Runtime
zurueckgelesen und gegen die torch-Ausgabe geprueft. Ein stiller
Export-Fehler — vertauschte Achsen, verlorene Normalisierung — wuerde sonst
erst in der Erkennungsgenauigkeit auffallen, wo er kaum zuzuordnen ist.
"""

from __future__ import annotations

import sys

import numpy as np
import torch

from .features import INPUT_CHANNELS
from .geometry import CANONICAL_SIDE
from .unet import UNet

# ONNX-Opset mit stabiler Unterstuetzung fuer GroupNorm und Resize; unter 18
# fehlt der Adapter fuer Resize und der Export faellt geraeuschvoll zurueck.
OPSET = 18

INPUT_NAME = "input"
OUTPUT_NAME = "logits"


def export(
    model: UNet,
    path: str,
    side: int = CANONICAL_SIDE,
    verify: bool = True,
    tolerance: float = 1e-3,
) -> str:
    """Schreibt das Netz als ONNX-Graph und prueft es gegen torch."""
    model.eval()
    example = torch.zeros(1, INPUT_CHANNELS, side, side)

    torch.onnx.export(
        model,
        example,
        path,
        input_names=[INPUT_NAME],
        output_names=[OUTPUT_NAME],
        opset_version=OPSET,
        dynamic_axes={
            INPUT_NAME: {0: "batch", 2: "height", 3: "width"},
            OUTPUT_NAME: {0: "batch", 2: "height", 3: "width"},
        },
    )

    if verify:
        _verify(model, path, side, tolerance)

    return path


def _verify(model: UNet, path: str, side: int, tolerance: float) -> None:
    import onnxruntime

    generator = torch.Generator().manual_seed(7)
    probe = torch.randn(1, INPUT_CHANNELS, side, side, generator=generator)

    with torch.no_grad():
        expected = model(probe).numpy()

    session = onnxruntime.InferenceSession(path, providers=["CPUExecutionProvider"])
    actual = session.run(None, {INPUT_NAME: probe.numpy()})[0]

    deviation = float(np.abs(expected - actual).max())

    if not np.isfinite(deviation) or deviation > tolerance:
        raise RuntimeError(
            f"ONNX-Export weicht von torch ab: max {deviation:.2e} > {tolerance:.0e}"
        )

    print(
        f"goteach-vision: ONNX geprueft, maximale Abweichung {deviation:.2e}",
        file=sys.stderr,
    )


def load_weights(model: UNet, path: str) -> UNet:
    model.load_state_dict(torch.load(path, map_location="cpu"))

    return model
