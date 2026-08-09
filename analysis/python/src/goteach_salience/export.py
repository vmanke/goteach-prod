"""ONNX-Export samt Gegenprobe.

Der Export wird sofort zurueckgelesen und gegen torch geprueft. Bei diesem
Netz ist das mehr als Formsache: Die Rueckkopplung zieht im Training eine
Richtung, in der Inferenz nimmt sie den Erwartungswert. Exportiert werden
muss der Inferenzpfad — schluepft versehentlich der stochastische Zweig in
den Graphen, liefert dieselbe Partie zweimal analysiert verschiedene
Ergebnisse, und das faellt ohne Gegenprobe erst sehr spaet auf.
"""

from __future__ import annotations

import sys

import numpy as np
import torch

from .features import INPUT_CHANNELS
from .feedback import FeedbackNet

OPSET = 18

INPUT_NAME = "input"
OUTPUT_NAME = "salience"


def export(
    model: FeedbackNet,
    path: str,
    size: int = 19,
    verify: bool = True,
    tolerance: float = 1e-3,
) -> str:
    model.eval()
    example = torch.zeros(1, INPUT_CHANNELS, size, size)

    torch.onnx.export(
        model,
        example,
        path,
        input_names=[INPUT_NAME],
        output_names=[OUTPUT_NAME],
        opset_version=OPSET,
    )

    if verify:
        _verify(model, path, size, tolerance)

    return path


def _verify(model: FeedbackNet, path: str, size: int, tolerance: float) -> None:
    import onnxruntime

    generator = torch.Generator().manual_seed(11)
    probe = torch.randn(1, INPUT_CHANNELS, size, size, generator=generator)

    with torch.no_grad():
        first = model(probe).numpy()
        second = model(probe).numpy()

    # Zuerst: Ist der Inferenzpfad ueberhaupt deterministisch?
    drift = float(np.abs(first - second).max())

    if drift > 1e-6:
        raise RuntimeError(
            f"Netz ist in der Inferenz nicht deterministisch (Abweichung {drift:.2e}) "
            "— die Rueckkopplung zieht noch eine Richtung, statt zu mitteln"
        )

    run = onnxruntime.InferenceSession(path, providers=["CPUExecutionProvider"])
    actual = run.run(None, {INPUT_NAME: probe.numpy()})[0]

    deviation = float(np.abs(first - actual).max())

    if not np.isfinite(deviation) or deviation > tolerance:
        raise RuntimeError(
            f"ONNX-Export weicht von torch ab: max {deviation:.2e} > {tolerance:.0e}"
        )

    print(
        f"goteach-salience: ONNX geprueft, deterministisch, "
        f"maximale Abweichung {deviation:.2e}",
        file=sys.stderr,
    )


def load_weights(model: FeedbackNet, path: str) -> FeedbackNet:
    model.load_state_dict(torch.load(path, map_location="cpu"))

    return model
