"""Das preinformed U-Net (Stufe 2).

Bewusst klein: rund 1,9 Millionen Parameter, damit Training auf einer CPU in
Reichweite bleibt und die Inferenz per ONNX Runtime schnell ist. Zwei
Bauentscheidungen sind kein Geschmack, sondern Zweck:

* **GroupNorm statt BatchNorm** — batchunabhaengig, also stabil bei den
  kleinen Batches, die auf bescheidener Hardware moeglich sind, und im
  ONNX-Graph ohne laufende Statistiken.
* **Bilineares Hochskalieren plus Faltung statt ConvTranspose** — keine
  Schachbrettmuster an den Steinraendern, und der Export bleibt einfach.

torch wird nur fuer Training und Export gebraucht; zur Laufzeit laeuft das
Netz als ONNX-Graph ohne torch.
"""

from __future__ import annotations

import torch
from torch import nn

from .features import INPUT_CHANNELS

# leer / schwarz / weiss
NUM_CLASSES = 3

# Kanalbreiten der Encoder-Stufen; der Flaschenhals verdoppelt die letzte.
WIDTHS = (16, 32, 64, 128)


def _norm(channels: int) -> nn.GroupNorm:
    return nn.GroupNorm(num_groups=min(8, channels), num_channels=channels)


class Block(nn.Module):
    """Zwei Faltungen mit Normalisierung und Aktivierung."""

    def __init__(self, in_channels: int, out_channels: int):
        super().__init__()

        self.body = nn.Sequential(
            nn.Conv2d(in_channels, out_channels, 3, padding=1, bias=False),
            _norm(out_channels),
            nn.SiLU(inplace=True),
            nn.Conv2d(out_channels, out_channels, 3, padding=1, bias=False),
            _norm(out_channels),
            nn.SiLU(inplace=True),
        )

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.body(x)


class UNet(nn.Module):
    """Encoder-Decoder mit Skip-Verbindungen, 6 Kanaele rein, 3 Klassen raus."""

    def __init__(self, in_channels: int = INPUT_CHANNELS, classes: int = NUM_CLASSES):
        super().__init__()

        self.downs = nn.ModuleList()
        previous = in_channels

        for width in WIDTHS:
            self.downs.append(Block(previous, width))
            previous = width

        self.pool = nn.MaxPool2d(2)
        self.bottleneck = Block(WIDTHS[-1], WIDTHS[-1] * 2)

        self.ups = nn.ModuleList()
        previous = WIDTHS[-1] * 2

        for width in reversed(WIDTHS):
            self.ups.append(Block(previous + width, width))
            previous = width

        self.head = nn.Conv2d(WIDTHS[0], classes, 1)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        skips = []

        for block in self.downs:
            x = block(x)
            skips.append(x)
            x = self.pool(x)

        x = self.bottleneck(x)

        for block, skip in zip(self.ups, reversed(skips)):
            x = nn.functional.interpolate(
                x, size=skip.shape[-2:], mode="bilinear", align_corners=False
            )
            x = block(torch.cat([x, skip], dim=1))

        return self.head(x)


def parameter_count(model: nn.Module) -> int:
    return sum(p.numel() for p in model.parameters())
