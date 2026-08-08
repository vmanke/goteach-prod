"""Training des preinformed U-Nets auf synthetischen Daten.

Der Verlust hat drei Anteile, und der dritte ist der wichtigste:

* gewichtete Cross-Entropy — "leer" macht rund 90 Prozent der Pixel aus und
  wuerde ungewichtet alles dominieren;
* Soft-Dice ueber die beiden Steinklassen — belohnt vollstaendige Steine
  statt nur richtig geratener Mehrheiten;
* Cross-Entropy *an den Schnittpunkten* — die Groesse, die am Ende
  tatsaechlich ausgelesen wird. Ohne sie optimiert das Netz eine Segmentierung,
  die als Bild gut aussieht, aber genau dort danebenliegt, wo es zaehlt.
"""

from __future__ import annotations

import sys
import time
from dataclasses import dataclass, field

import numpy as np
import torch
from torch import nn

from .contract import BOARD_SIZES
from .geometry import CANONICAL_SIDE
from .grid import DEFAULT_MARGIN, intersections
from .postprocess import decide, pool_logits
from .render import dataset
from .unet import NUM_CLASSES, UNet, parameter_count

# Gewichte der Klassen leer/schwarz/weiss im Cross-Entropy.
CLASS_WEIGHTS = (0.25, 1.0, 1.0)

# Anteil der drei Verlustanteile.
DICE_WEIGHT = 0.5
POINT_WEIGHT = 1.0


@dataclass
class Metrics:
    """Auswertung, getrennt nach Domaene."""

    points_correct: dict[str, int] = field(default_factory=dict)
    points_total: dict[str, int] = field(default_factory=dict)
    boards_exact: dict[str, int] = field(default_factory=dict)
    boards_total: dict[str, int] = field(default_factory=dict)

    def add(self, domain: str, predicted: np.ndarray, truth: np.ndarray) -> None:
        hits = int((predicted == truth).sum())
        self.points_correct[domain] = self.points_correct.get(domain, 0) + hits
        self.points_total[domain] = self.points_total.get(domain, 0) + truth.size
        self.boards_exact[domain] = self.boards_exact.get(domain, 0) + int(
            (predicted == truth).all()
        )
        self.boards_total[domain] = self.boards_total.get(domain, 0) + 1

    def report(self) -> str:
        parts = []

        for domain in sorted(self.points_total):
            accuracy = self.points_correct[domain] / max(1, self.points_total[domain])
            exact = self.boards_exact[domain] / max(1, self.boards_total[domain])
            parts.append(f"{domain}: Punkte {accuracy:.4f}, Bretter exakt {exact:.3f}")

        return " | ".join(parts) if parts else "keine Daten"


def train(
    steps: int = 2000,
    batch: int = 4,
    learning_rate: float = 2e-3,
    photo_ratio: float = 0.6,
    side: int = CANONICAL_SIDE,
    margin: float = DEFAULT_MARGIN,
    validate_every: int = 250,
    validation_size: int = 24,
    output: str | None = None,
    seed_offset: int = 0,
    device: str = "cpu",
) -> UNet:
    """Trainiert das Netz und gibt es zurueck."""
    torch.manual_seed(12345 + seed_offset)

    model = UNet().to(device)
    optimiser = torch.optim.AdamW(model.parameters(), lr=learning_rate)
    schedule = torch.optim.lr_scheduler.CosineAnnealingLR(optimiser, T_max=max(1, steps))

    print(
        f"goteach-vision: U-Net mit {parameter_count(model):,} Parametern, "
        f"{steps} Schritte, Batch {batch}, Foto-Anteil {photo_ratio:.2f}",
        file=sys.stderr,
    )

    weights = torch.tensor(CLASS_WEIGHTS, dtype=torch.float32, device=device)
    started = time.time()

    for step in range(1, steps + 1):
        model.train()
        tensors, masks, samples = _batch(
            dataset.seeds(dataset.TRAIN_SEEDS, batch, seed_offset + step * batch),
            photo_ratio,
            side,
            margin,
        )

        inputs = torch.from_numpy(tensors).to(device)
        targets = torch.from_numpy(masks).to(device)
        prediction = model(inputs)

        loss = _loss(prediction, targets, samples, weights, side, margin, device)

        optimiser.zero_grad(set_to_none=True)
        loss.backward()
        torch.nn.utils.clip_grad_norm_(model.parameters(), 1.0)
        optimiser.step()
        schedule.step()

        if step % 25 == 0 or step == 1:
            print(
                f"goteach-vision: Schritt {step}/{steps} "
                f"Verlust {loss.item():.4f} ({time.time() - started:.0f}s)",
                file=sys.stderr,
            )

        if validate_every and (step % validate_every == 0 or step == steps):
            metrics = evaluate(model, validation_size, side, margin, device)
            print(f"goteach-vision: Validierung — {metrics.report()}", file=sys.stderr)

    if output:
        torch.save(model.state_dict(), output)
        print(f"goteach-vision: Gewichte -> {output}", file=sys.stderr)

    return model


def _batch(seeds, photo_ratio, side, margin):
    tensors, masks, samples = [], [], []

    for seed in seeds:
        tensor, mask, raw = dataset.sample(
            seed, photo_ratio=photo_ratio, side=side, margin=margin
        )
        tensors.append(tensor)
        masks.append(mask)
        samples.append(raw)

    return np.stack(tensors), np.stack(masks), samples


def _loss(prediction, targets, samples, weights, side, margin, device):
    pixel = nn.functional.cross_entropy(prediction, targets, weight=weights)
    dice = _dice(prediction, targets)
    point = _point_loss(prediction, samples, weights, side, margin, device)

    return pixel + DICE_WEIGHT * dice + POINT_WEIGHT * point


def _dice(prediction, targets, epsilon: float = 1.0):
    """Soft-Dice ueber die beiden Steinklassen."""
    probability = prediction.softmax(dim=1)
    total = 0.0

    for label in (1, 2):
        predicted = probability[:, label]
        truth = (targets == label).float()
        overlap = (predicted * truth).sum(dim=(1, 2))
        volume = predicted.sum(dim=(1, 2)) + truth.sum(dim=(1, 2))
        total = total + (1.0 - (2 * overlap + epsilon) / (volume + epsilon)).mean()

    return total / 2.0


def _point_loss(prediction, samples, weights, side, margin, device):
    """Cross-Entropy auf der 3x3-Umgebung jedes Schnittpunkts.

    Genau dort liest die Nachverarbeitung spaeter aus; ein Netz, das nur den
    Bildeindruck optimiert, darf hier nicht davonkommen.
    """
    total = torch.zeros((), device=device)

    for index, raw in enumerate(samples):
        ys, xs = _intersection_pixels(raw.size, side, margin)
        gathered = []

        for dy in (-1, 0, 1):
            for dx in (-1, 0, 1):
                yy = np.clip(ys + dy, 0, side - 1)
                xx = np.clip(xs + dx, 0, side - 1)
                gathered.append(prediction[index, :, yy, xx])

        logits = torch.stack(gathered, dim=0).mean(dim=0).transpose(0, 1)
        truth = torch.from_numpy(
            np.asarray(raw.labels, dtype=np.int64).ravel()
        ).to(device)

        total = total + nn.functional.cross_entropy(logits, truth, weight=weights)

    return total / max(1, len(samples))


_PIXEL_CACHE: dict[tuple[int, int, float], tuple[np.ndarray, np.ndarray]] = {}


def _intersection_pixels(size: int, side: int, margin: float):
    key = (size, side, margin)

    if key not in _PIXEL_CACHE:
        points = intersections(size, side, margin)
        _PIXEL_CACHE[key] = (
            np.round(points[:, 1]).astype(np.int64),
            np.round(points[:, 0]).astype(np.int64),
        )

    return _PIXEL_CACHE[key]


@torch.no_grad()
def evaluate(
    model: UNet,
    count: int = 24,
    side: int = CANONICAL_SIDE,
    margin: float = DEFAULT_MARGIN,
    device: str = "cpu",
) -> Metrics:
    """Bewertet mit derselben Auslese, die auch produktiv laeuft."""
    model.eval()
    metrics = Metrics()

    for offset, seed in enumerate(dataset.seeds(dataset.VALIDATION_SEEDS, count)):
        # Domaenen bewusst abwechseln, damit beide gleich stark vertreten sind.
        tensor, _, raw = dataset.sample(
            seed, photo_ratio=1.0 if offset % 2 else 0.0, side=side, margin=margin
        )
        logits = model(torch.from_numpy(tensor[None]).to(device))[0].cpu().numpy()
        labels, _ = decide(pool_logits(logits, raw.size, margin), raw.size)
        metrics.add(raw.domain, labels, np.asarray(raw.labels))

    return metrics


def board_sizes() -> tuple[int, ...]:
    return BOARD_SIZES


def num_classes() -> int:
    return NUM_CLASSES
