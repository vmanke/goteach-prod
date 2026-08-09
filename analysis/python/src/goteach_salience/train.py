"""Selbstueberwachtes Training des Salienzmoduls.

Es gibt keine Labels fuer "narrativ interessant" — und es kann keine geben,
ohne dass jemand hunderte Partien von Hand markiert. Deshalb lernt das Netz
eine Groesse, die sich aus den Daten selbst ergibt: **wie stark sich die
Zugehoerigkeit eines Punktes in den naechsten Zuegen noch veraendert**,
vorhergesagt aus der jetzigen Stellung samt kurzer Historie.

Das ist genau die Frage, die die Fensterung braucht ("wo entscheidet sich
noch etwas?"), und sie hat eine nachpruefbare Antwort in jeder Partie.
"""

from __future__ import annotations

import sys
import time

import numpy as np
import torch
from torch import nn

from .contract import Game
from .features import HORIZON, build_input, target, usable_turns
from .feedback import FeedbackNet, parameter_count


def synthetic_game(seed: int, size: int = 19, turns: int = 80) -> Game:
    """Erzeugt eine Partie fuer Rauchtests und die CI.

    Kein Ersatz fuer echte Partien: Die Ownership-Felder entstehen hier aus
    einem Einflussmodell, nicht aus einem neuronalen Netz. Fuer die Pruefung,
    ob Training, Export und Inferenz zusammenpassen, genuegt das — fuer eine
    Aussage ueber die Genauigkeit nicht.
    """
    from .contract import Turn

    rng = np.random.default_rng(seed)
    board = np.zeros((size, size), dtype=np.int8)
    frames = []

    for t in range(turns):
        # Zuege wandern in Schueben ueber das Brett, damit es Gegenden mit
        # zeitlich begrenzter Aktivitaet gibt.
        centre = (
            int(size * (0.25 + 0.5 * ((t // 20) % 2))),
            int(size * (0.25 + 0.5 * ((t // 13) % 2))),
        )
        y = int(np.clip(rng.normal(centre[0], 2.0), 0, size - 1))
        x = int(np.clip(rng.normal(centre[1], 2.0), 0, size - 1))

        if board[y, x] == 0:
            board[y, x] = 1 if t % 2 == 0 else 2

        ownership = _influence(board, size)
        rows = tuple(
            "".join(".XO"[int(v)] for v in board[row]) for row in range(size)
        )
        frames.append(Turn(rows=rows, ownership=ownership))

    return Game(size=size, turns=tuple(frames))


def _influence(board: np.ndarray, size: int) -> np.ndarray:
    """Dichtes Einflussfeld ueber alle Steine."""
    coords = np.indices((size, size)).astype(np.float32)
    field = np.zeros((size, size), dtype=np.float32)

    for value, sign in ((1, 1.0), (2, -1.0)):
        for y, x in zip(*np.nonzero(board == value)):
            distance = np.abs(coords[0] - y) + np.abs(coords[1] - x)
            field += sign * np.exp(-distance / 3.5)

    return np.tanh(field).astype(np.float32)


def train(
    steps: int = 400,
    batch: int = 4,
    learning_rate: float = 2e-3,
    games: int = 8,
    turns: int = 80,
    size: int = 19,
    output: str | None = None,
    device: str = "cpu",
) -> FeedbackNet:
    """Trainiert das Netz auf synthetischen Partien."""
    torch.manual_seed(20260809)

    model = FeedbackNet().to(device)
    optimiser = torch.optim.AdamW(model.parameters(), lr=learning_rate)

    print(
        f"goteach-salience: Netz mit {parameter_count(model):,} Parametern, "
        f"{steps} Schritte, Batch {batch}, Horizont {HORIZON}",
        file=sys.stderr,
    )

    corpus = [synthetic_game(seed, size=size, turns=turns) for seed in range(games)]
    rng = np.random.default_rng(7)
    started = time.time()

    for step in range(1, steps + 1):
        model.train()
        inputs, targets = _batch(corpus, rng, batch)

        prediction = model(torch.from_numpy(inputs).to(device))[:, 0]
        truth = torch.from_numpy(targets).to(device)

        loss = nn.functional.smooth_l1_loss(prediction, truth)

        optimiser.zero_grad(set_to_none=True)
        loss.backward()
        torch.nn.utils.clip_grad_norm_(model.parameters(), 1.0)
        optimiser.step()

        if step % 50 == 0 or step == 1:
            print(
                f"goteach-salience: Schritt {step}/{steps} "
                f"Verlust {loss.item():.5f} ({time.time() - started:.0f}s)",
                file=sys.stderr,
            )

    if output:
        torch.save(model.state_dict(), output)
        print(f"goteach-salience: Gewichte -> {output}", file=sys.stderr)

    return model


def _batch(corpus: list[Game], rng: np.random.Generator, batch: int):
    inputs, targets = [], []

    while len(inputs) < batch:
        game = corpus[int(rng.integers(len(corpus)))]
        options = list(usable_turns(game))

        if not options:
            continue

        turn = options[int(rng.integers(len(options)))]
        inputs.append(build_input(game, turn))
        targets.append(target(game, turn))

    return np.stack(inputs), np.stack(targets)


@torch.no_grad()
def correlation_with_observed(model: FeedbackNet, game: Game) -> float:
    """Wie gut folgt die Vorhersage der tatsaechlichen Umwaelzung?

    Eine Plausibilitaetspruefung, kein Genauigkeitsmass: Sie sagt, ob das
    Netz ueberhaupt etwas gelernt hat, nicht ob die Fenster die richtigen
    sind.
    """
    model.eval()
    predicted, observed = [], []

    for turn in usable_turns(game):
        tensor = torch.from_numpy(build_input(game, turn)[None])
        predicted.append(model(tensor)[0, 0].numpy().ravel())
        observed.append(target(game, turn).ravel())

    if not predicted:
        return 0.0

    a = np.concatenate(predicted)
    b = np.concatenate(observed)

    if a.std() < 1e-9 or b.std() < 1e-9:
        return 0.0

    return float(np.corrcoef(a, b)[0, 1])
