"""Das Netz mit bidirektionaler Rückkopplung.

Braucht torch und wird ohne torch übersprungen — der Vertrag, die Merkmale
und die Fensterung müssen ohne ML prüfbar bleiben.
"""

from __future__ import annotations

import numpy as np
import pytest

from goteach_salience.features import INPUT_CHANNELS
from goteach_salience.train import synthetic_game

torch = pytest.importorskip("torch", reason="torch nur für Training/Export nötig")

pytestmark = pytest.mark.torch


def net(**kwargs):
    from goteach_salience.feedback import FeedbackNet

    return FeedbackNet(**kwargs)


def test_netz_erhaelt_die_brettgroesse():
    from goteach_salience.feedback import parameter_count

    model = net()
    out = model(torch.zeros(2, INPUT_CHANNELS, 19, 19))

    assert out.shape == (2, 1, 19, 19)
    assert parameter_count(model) < 2_000_000


def test_inferenz_ist_deterministisch():
    """Zweimal dieselbe Eingabe muss zweimal dasselbe ergeben.

    Das ist die zentrale Eigenschaft der Konstruktion: Im Training wird die
    Rückkopplungsrichtung gezogen, in der Inferenz wird über beide Richtungen
    gemittelt. Ohne diese Umschaltung wäre dieselbe Partie zweimal analysiert
    nicht zweimal dieselbe Analyse — und das verträgt sich nicht mit einem
    Teaching, das reproduzierbar sein soll.
    """
    model = net()
    model.eval()

    probe = torch.randn(1, INPUT_CHANNELS, 13, 13, generator=torch.Generator().manual_seed(5))

    with torch.no_grad():
        first = model(probe)
        second = model(probe)

    assert torch.allclose(first, second, atol=0.0)


def test_training_zieht_die_richtung_tatsaechlich():
    """Im Trainingsmodus darf das Ergebnis schwanken — sonst gäbe es keine
    stochastische Richtungswahl, und der Erwartungswert in der Inferenz wäre
    eine Antwort auf eine Frage, die niemand gestellt hat."""
    model = net()
    model.train()

    probe = torch.randn(8, INPUT_CHANNELS, 13, 13, generator=torch.Generator().manual_seed(6))

    torch.manual_seed(1)
    first = model(probe)
    torch.manual_seed(2)
    second = model(probe)

    assert not torch.allclose(first, second, atol=1e-6)


def test_rueckkopplung_wirkt_ueberhaupt():
    # Mit geschlossenem Tor darf die Rückkopplung nichts ändern; das prüft,
    # dass sie im Normalfall tatsächlich am Ergebnis beteiligt ist.
    model = net()
    model.eval()

    probe = torch.randn(1, INPUT_CHANNELS, 13, 13, generator=torch.Generator().manual_seed(7))

    with torch.no_grad():
        before = model(probe).clone()

        model.gates.fill_(-20.0)  # sigmoid ≈ 0
        after = model(probe)

    assert not torch.allclose(before, after, atol=1e-5)


def test_mehr_durchlaeufe_aendern_das_ergebnis():
    once = net(iterations=1)
    thrice = net(iterations=3)
    thrice.load_state_dict(once.state_dict())

    once.eval()
    thrice.eval()

    probe = torch.randn(1, INPUT_CHANNELS, 13, 13, generator=torch.Generator().manual_seed(8))

    with torch.no_grad():
        assert not torch.allclose(once(probe), thrice(probe), atol=1e-5)


def test_export_ist_deterministisch_und_stimmt_mit_torch(tmp_path):
    pytest.importorskip("onnxruntime")
    pytest.importorskip("onnxscript")

    from goteach_salience.export import export

    path = tmp_path / "salience.onnx"

    # export() prüft selbst beides und wirft bei Abweichung.
    export(net(), str(path), size=13, tolerance=1e-3)

    assert path.exists()


def test_training_senkt_den_verlust():
    from goteach_salience.train import train

    model = train(steps=30, batch=2, games=2, turns=40, size=13)

    assert model is not None


def test_vorhersage_folgt_der_beobachtung_nach_dem_training():
    """Plausibilitätsprüfung, kein Genauigkeitsmaß.

    Nach ein paar hundert Schritten auf synthetischen Partien soll die
    Vorhersage der tatsächlichen Umwälzung wenigstens folgen. Ob die Fenster
    daraus die *richtigen* sind, sagt dieser Test ausdrücklich nicht.
    """
    from goteach_salience.train import correlation_with_observed, train

    model = train(steps=150, batch=4, games=3, turns=50, size=13)
    game = synthetic_game(99, size=13, turns=50)

    assert correlation_with_observed(model, game) > 0.2


def test_onnx_pfad_liefert_dieselben_fenster_wie_torch(tmp_path):
    pytest.importorskip("onnxruntime")
    pytest.importorskip("onnxscript")

    from goteach_salience.export import export
    from goteach_salience.infer import predicted_salience
    from goteach_salience.windows import find_windows

    path = tmp_path / "salience.onnx"
    model = net()
    export(model, str(path), size=13)

    game = synthetic_game(11, size=13, turns=40)
    field = predicted_salience(game, str(path))

    assert field.shape == (len(game.turns), 13, 13)
    assert np.isfinite(field).all()

    # Zweimal aufgerufen muss dasselbe herauskommen.
    again = predicted_salience(game, str(path))
    assert np.array_equal(field, again)

    first = find_windows(game, field)
    second = find_windows(game, again)

    assert [w.to_dict() for w in first] == [w.to_dict() for w in second]
