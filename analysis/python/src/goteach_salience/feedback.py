"""Das Netz mit bidirektionaler Rueckkopplung.

Die Bauform folgt dem Predictive-Coding-Gedanken: An jeder Ebene trifft ein
Signal von unten (Kodierer-Richtung) auf eines von oben (Dekoder-Richtung).
Welches davon eine Ebene in einem Durchlauf bekommt, wird *zufaellig*
gezogen — genau an der Nichtlinearitaet, an der es wirkt.

Zwei Entscheidungen sind kein Geschmack, sondern Notwendigkeit:

* **Feste Zahl von Durchlaeufen.** Ein unbestimmter Fixpunkt haette keinen
  definierten ONNX-Graphen. Wer echte Konvergenz will, nimmt ein Deep
  Equilibrium Model mit implizitem Gradienten statt das Netz zu entrollen.
* **Erwartungswert statt Stichprobe zur Laufzeit.** Im Training wird die
  Richtung gezogen, in der Inferenz werden beide Richtungen mit ihrer
  Wahrscheinlichkeit gewichtet — genau wie Dropout. Ohne das ergaebe dieselbe
  Partie zweimal analysiert nicht dasselbe Ergebnis, und ein Projekt, dessen
  Teaching reproduzierbar sein soll, kann sich das nicht leisten.
"""

from __future__ import annotations

import torch
from torch import nn

from .features import INPUT_CHANNELS

# Kanalbreiten der Ebenen, von fein nach grob.
WIDTHS = (16, 32, 64)

# So oft laeuft die Rueckkopplung.
ITERATIONS = 3

# Wahrscheinlichkeit, dass eine Ebene das Signal von unten bekommt.
ENCODER_PROBABILITY = 0.5


def _norm(channels: int) -> nn.GroupNorm:
    return nn.GroupNorm(num_groups=min(8, channels), num_channels=channels)


class Block(nn.Module):
    """Faltung, Normalisierung, Aktivierung — die Stelle, an der die
    Rueckkopplung ansetzt."""

    def __init__(self, in_channels: int, out_channels: int):
        super().__init__()

        self.body = nn.Sequential(
            nn.Conv2d(in_channels, out_channels, 3, padding=1, bias=False),
            _norm(out_channels),
            nn.SiLU(inplace=True),
        )

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        return self.body(x)


class FeedbackNet(nn.Module):
    """Salienzvorhersage mit bidirektionaler Rueckkopplung."""

    def __init__(
        self,
        in_channels: int = INPUT_CHANNELS,
        widths: tuple[int, ...] = WIDTHS,
        iterations: int = ITERATIONS,
        encoder_probability: float = ENCODER_PROBABILITY,
    ):
        super().__init__()

        self.widths = widths
        self.iterations = iterations
        self.encoder_probability = encoder_probability

        self.stem = Block(in_channels, widths[0])

        # Je Ebene ein Block, der Zustand und Rueckkopplung zusammenfuehrt.
        self.levels = nn.ModuleList(
            Block(widths[i] * 2, widths[i]) for i in range(len(widths))
        )

        # Uebergaenge zwischen benachbarten Ebenen, in beide Richtungen.
        self.down = nn.ModuleList(
            nn.Conv2d(widths[i], widths[i + 1], 3, stride=2, padding=1)
            for i in range(len(widths) - 1)
        )
        self.up = nn.ModuleList(
            nn.Conv2d(widths[i + 1], widths[i], 1) for i in range(len(widths) - 1)
        )

        # Ein gelerntes Tor je Ebene entscheidet, wie stark die Rueckkopplung
        # ueberhaupt wirkt.
        self.gates = nn.Parameter(torch.zeros(len(widths)))

        self.head = nn.Conv2d(widths[0], 1, 1)

    def forward(self, x: torch.Tensor) -> torch.Tensor:
        state = [self.stem(x)]

        for level in range(1, len(self.widths)):
            state.append(self.down[level - 1](state[-1]))

        for _ in range(self.iterations):
            state = self._iterate(state)

        return self.head(state[0])

    def _iterate(self, state: list[torch.Tensor]) -> list[torch.Tensor]:
        updated = []

        for level, current in enumerate(state):
            feedback = self._feedback(state, level, current)
            gate = torch.sigmoid(self.gates[level])
            merged = torch.cat([current, gate * feedback], dim=1)
            updated.append(self.levels[level](merged) + current)

        return updated

    def _feedback(
        self, state: list[torch.Tensor], level: int, current: torch.Tensor
    ) -> torch.Tensor:
        """Das Signal, das an dieser Ebene ankommt.

        Von unten heisst: aus der feineren Ebene heraufgereicht (Kodierer-
        Richtung). Von oben heisst: aus der groeberen Ebene zurueck (Dekoder-
        Richtung). An den Raendern gibt es nur eine Seite.
        """
        from_below = (
            self.down[level - 1](state[level - 1]) if level > 0 else None
        )
        from_above = (
            self._upsample(state[level + 1], level, current)
            if level + 1 < len(state)
            else None
        )

        if from_below is None:
            return from_above if from_above is not None else torch.zeros_like(current)

        if from_above is None:
            return from_below

        if self.training:
            # Richtung ziehen — je Beispiel, damit ein Batch beide Wege sieht.
            draw = torch.rand(
                current.shape[0], 1, 1, 1, device=current.device, dtype=current.dtype
            )
            take_below = (draw < self.encoder_probability).to(current.dtype)

            return take_below * from_below + (1.0 - take_below) * from_above

        # Inferenz: Erwartungswert statt Stichprobe.
        p = self.encoder_probability

        return p * from_below + (1.0 - p) * from_above

    def _upsample(
        self, coarse: torch.Tensor, level: int, like: torch.Tensor
    ) -> torch.Tensor:
        projected = self.up[level](coarse)

        return nn.functional.interpolate(
            projected, size=like.shape[-2:], mode="bilinear", align_corners=False
        )


def parameter_count(model: nn.Module) -> int:
    return sum(p.numel() for p in model.parameters())
