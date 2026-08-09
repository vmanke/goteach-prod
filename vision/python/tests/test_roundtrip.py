"""Ende-zu-Ende: gerendertes Brett -> Pipeline -> symbolische Stellung.

Der Abnahmetest der Screenshot-Domaene, ohne jedes Modellgewicht — das
klassische Backend genuegt und macht die Pruefung schnell und reproduzierbar.

Gemessen wird *getrennt*, was getrennt versagt: die Entzerrung (Stufe 1) und
die Auslese (Stufe 2). Eine einzige Ende-zu-Ende-Zahl wuerde verschleiern,
dass die Auslese praktisch fehlerfrei ist und der verbleibende Fehler fast
vollstaendig aus der Rahmensuche stammt — die Stelle also, an der eine
Verbesserung tatsaechlich lohnt.

Die Schwarzweiss-Druckpalette fehlt bewusst: dort ist ein weisser Stein innen
exakt so hell wie das Papier, und nur seine Kontur verraet ihn. Dafuer ist
das U-Net zustaendig, nicht das klassische Backend (siehe classical.py).
"""

from __future__ import annotations

import numpy as np
import pytest

from goteach_vision.contract import BOARD_SIZES
from goteach_vision.pipeline import detect_position
from goteach_vision.render.position import strip_dead
from goteach_vision.render.screenshot import render_screenshot

# Holzbretter hell/mittel/warm und Dunkelmodus.
REALISTIC_PALETTES = (0, 1, 3, 4)

# Ein Rahmen gilt als getroffen, wenn keine Ecke weiter als ein Fuenftel der
# Gitterteilung danebenliegt — die Auslese-Scheibe (0.30) laege dann immer
# noch sicher im Stein (0.46).
FRAME_TOLERANCE = 0.2


def _evaluate(size: int, palette: int, run: int):
    rng = np.random.default_rng(1000 + size * 10 + palette + run * 997)
    sample = render_screenshot(rng, size, palette=palette)

    detection = detect_position(sample.image, size_hint=size)
    recovered = detection.position.to_labels()

    pitch = float(abs(sample.corners[1, 0] - sample.corners[0, 0])) / (size - 1)
    frame_error = float(
        np.abs(detection.geometry.corners - sample.corners).max()
    ) / pitch

    return frame_error, recovered, sample.labels


def _sweep(runs: int = 6):
    for size in BOARD_SIZES:
        for palette in REALISTIC_PALETTES:
            for run in range(runs):
                yield _evaluate(size, palette, run)


def test_auslese_ist_exakt_wenn_der_rahmen_sitzt():
    """Stufe 2 allein: bei getroffenem Rahmen muss die Stellung stimmen."""
    accuracies = [
        float((recovered == truth).mean())
        for error, recovered, truth in _sweep()
        if error < FRAME_TOLERANCE
    ]

    assert len(accuracies) >= 30, "zu wenige brauchbare Rahmen fuer die Aussage"

    exact = float(np.mean([a == 1.0 for a in accuracies]))

    assert float(np.mean(accuracies)) > 0.99
    assert exact > 0.90


def test_rahmen_wird_ueberwiegend_gefunden():
    """Stufe 1 allein — und zugleich die dokumentierte offene Schwaeche.

    Die Rahmensuche verfehlt einen Teil der Bretter um eine ganze
    Gitterteilung, weil sie die Brettkante statt der ersten Gitterlinie
    nimmt. Die Schranke haelt den erreichten Stand fest, damit ein
    Rueckschritt auffaellt; sie ist kein Zielwert.
    """
    errors = [error for error, _, _ in _sweep()]
    hit = float(np.mean([e < FRAME_TOLERANCE for e in errors]))

    assert hit > 0.60


@pytest.mark.parametrize("palette", REALISTIC_PALETTES)
def test_leeres_brett_bleibt_leer(palette):
    rng = np.random.default_rng(77)
    sample = render_screenshot(
        rng, 19, labels=np.zeros((19, 19), dtype=np.int8), palette=palette
    )

    assert detect_position(sample.image, size_hint=19).position.stones() == 0


@pytest.mark.parametrize("palette", REALISTIC_PALETTES)
def test_nur_eine_farbe_erzeugt_keine_gegenfarbe(palette):
    """Ein Brett mit ausschliesslich weissen Steinen darf kein Schwarz melden.

    Ohne diese Pruefung faellt eine ganze Fehlerklasse durch: Sobald der
    Bezugswert des Bretts verrutscht oder ein Nachweis fuer "verdeckte"
    Steine zu grosszuegig greift, erscheinen leere Punkte als Steine — und
    zwar reihenweise, nicht vereinzelt.
    """
    rng = np.random.default_rng(23)
    labels = strip_dead(np.where(rng.random((19, 19)) < 0.2, 2, 0).astype(np.int8))
    sample = render_screenshot(rng, 19, labels=labels, palette=palette)

    recovered = detect_position(sample.image, size_hint=19).position.to_labels()

    assert not (recovered == 1).any()
    assert np.array_equal(recovered, labels), _diff(recovered, labels)


def test_json_geht_ohne_umweg_in_den_go_vertrag():
    sample = render_screenshot(np.random.default_rng(9), 9, palette=0)
    detection = detect_position(sample.image, size_hint=9, komi=6.5)

    payload = detection.position.to_json()

    assert '"size": 9' in payload
    assert '"komi": 6.5' in payload


def _diff(recovered: np.ndarray, truth: np.ndarray) -> str:
    wrong = np.argwhere(recovered != truth)

    return "abweichende Punkte: " + ", ".join(
        f"({y},{x}) erwartet {truth[y, x]} erhalten {recovered[y, x]}"
        for y, x in wrong[:12]
    )
