"""Kommandozeile von goteach-salience.

``score`` liest den Partieverlauf als JSON und schreibt die Fenster als JSON
nach stdout — alles andere geht nach stderr, damit die Ausgabe ohne Filter
weitergereicht werden kann.
"""

from __future__ import annotations

import argparse
import sys
import time

from .contract import Game, windows_to_json

EXIT_OK = 0
EXIT_ERROR = 1


def main(argv: list[str] | None = None) -> int:
    parser = _parser()
    args = parser.parse_args(argv)

    if not getattr(args, "command", None):
        parser.print_help()

        return EXIT_ERROR

    try:
        return args.handler(args)

    except (OSError, ValueError, KeyError) as error:
        print(f"goteach-salience: {error}", file=sys.stderr)

        return EXIT_ERROR


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="goteach-salience",
        description="Findet die brisanten Fenster einer Partie.",
    )
    sub = parser.add_subparsers(dest="command")

    score = sub.add_parser("score", help="Fenster einer Partie bestimmen")
    score.add_argument("game", help="JSON-Datei oder '-' fuer stdin")
    score.add_argument("--weights", help="ONNX-Modell; ohne das der beobachtete Pfad")
    score.add_argument("--top", type=int, default=8)
    score.add_argument("--quantile", type=float, default=None)
    score.set_defaults(handler=_score)

    train = sub.add_parser("train", help="Netz auf synthetischen Partien trainieren")
    train.add_argument("--steps", type=int, default=400)
    train.add_argument("--batch", type=int, default=4)
    train.add_argument("--games", type=int, default=8)
    train.add_argument("--out", help="Gewichte (.pt) schreiben")
    train.add_argument("--onnx", help="direkt nach ONNX exportieren")
    train.add_argument("--tiny", action="store_true", help="Rauchtest")
    train.set_defaults(handler=_train)

    export = sub.add_parser("export", help="Gewichte nach ONNX exportieren")
    export.add_argument("--out", required=True)
    export.add_argument("--state")
    export.add_argument("--size", type=int, default=19)
    export.set_defaults(handler=_export)

    return parser


def _score(args) -> int:
    from .infer import salience
    from .windows import DEFAULT_QUANTILE, find_windows

    started = time.time()

    if args.game == "-":
        game = Game.from_json(sys.stdin.read())
    else:
        with open(args.game, "r", encoding="utf-8") as handle:
            game = Game.from_json(handle.read())

    field = salience(game, args.weights)
    quantile = args.quantile if args.quantile is not None else DEFAULT_QUANTILE
    windows = find_windows(game, field, quantile=quantile, top=args.top)

    print(windows_to_json(windows))

    print(
        "goteach-salience: {turns} Stellungen, {count} Fenster, "
        "Modus {mode}, {seconds:.2f}s".format(
            turns=len(game.turns),
            count=len(windows),
            mode="onnx" if args.weights else "beobachtet",
            seconds=time.time() - started,
        ),
        file=sys.stderr,
    )

    return EXIT_OK


def _train(args) -> int:
    from .train import train

    steps = 20 if args.tiny else args.steps
    games = 2 if args.tiny else args.games

    model = train(steps=steps, batch=args.batch, games=games, output=args.out)

    if args.onnx:
        from .export import export

        export(model, args.onnx)

    return EXIT_OK


def _export(args) -> int:
    from .export import export, load_weights
    from .feedback import FeedbackNet

    model = FeedbackNet()

    if args.state:
        load_weights(model, args.state)

    export(model, args.out, size=args.size)
    print(f"goteach-salience: ONNX -> {args.out}", file=sys.stderr)

    return EXIT_OK


if __name__ == "__main__":
    raise SystemExit(main())
