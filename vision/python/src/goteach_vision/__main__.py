"""Kommandozeile von goteach-vision.

``detect`` schreibt ausschliesslich den JSON-Vertrag der Go-Seite nach
stdout — Konfidenz, erkannte Groesse und Laufzeit gehen nach stderr, genau
wie goteach selbst seine Hinweise meldet. So laesst sich die Ausgabe ohne
Filter weiterreichen.
"""

from __future__ import annotations

import argparse
import sys
import time

import numpy as np

from .contract import BOARD_SIZES
from .geometry import BoardNotFound
from .pngio import load_rgb, save_rgb

# Exitcodes: 0 erkannt, 1 Fehler, 2 unterhalb der geforderten Konfidenz.
EXIT_OK = 0
EXIT_ERROR = 1
EXIT_UNSURE = 2


def main(argv: list[str] | None = None) -> int:
    parser = _parser()
    args = parser.parse_args(argv)

    if not getattr(args, "command", None):
        parser.print_help()

        return EXIT_ERROR

    try:
        return args.handler(args)

    except BoardNotFound as error:
        print(f"goteach-vision: kein Brett erkannt: {error}", file=sys.stderr)

        return EXIT_ERROR

    except (OSError, ValueError) as error:
        print(f"goteach-vision: {error}", file=sys.stderr)

        return EXIT_ERROR


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="goteach-vision",
        description="Erkennt Go-Stellungen in PNG-Bildern (Stufen 1-2).",
    )
    sub = parser.add_subparsers(dest="command")

    detect = sub.add_parser("detect", help="Stellung aus einem PNG erkennen")
    detect.add_argument("image", help="PNG-Datei oder '-' fuer stdin")
    detect.add_argument(
        "--size", type=int, choices=BOARD_SIZES, help="Brettgroesse erzwingen"
    )
    detect.add_argument("--komi", type=float, help="Komi in die Ausgabe schreiben")
    detect.add_argument(
        "--backend", default="auto", choices=("auto", "onnx", "classical")
    )
    detect.add_argument("--weights", help="ONNX-Modell fuer das Backend 'onnx'")
    detect.add_argument(
        "--min-confidence",
        type=float,
        default=0.0,
        help="unterhalb dieser Konfidenz mit Exitcode 2 abbrechen",
    )
    detect.add_argument("--debug", help="Overlay-PNG zur Sichtpruefung schreiben")
    detect.add_argument("--json", help="Ausgabe zusaetzlich in eine Datei schreiben")
    detect.set_defaults(handler=_detect)

    render = sub.add_parser("render", help="synthetisches Brett erzeugen")
    render.add_argument("--out", required=True, help="Ziel-PNG")
    render.add_argument("--size", type=int, choices=BOARD_SIZES, default=19)
    render.add_argument("--domain", choices=("screenshot", "photo"), default="screenshot")
    render.add_argument("--seed", type=int, default=0)
    render.add_argument("--palette", type=int, help="Palette der Screenshot-Domaene")
    render.add_argument("--labels", help="Ground Truth als JSON danebenschreiben")
    render.set_defaults(handler=_render)

    train = sub.add_parser("train", help="U-Net auf synthetischen Daten trainieren")
    train.add_argument("--steps", type=int, default=2000)
    train.add_argument("--batch", type=int, default=4)
    train.add_argument("--lr", type=float, default=2e-3)
    train.add_argument("--photo-ratio", type=float, default=0.6)
    train.add_argument("--out", help="Gewichte (.pt) schreiben")
    train.add_argument("--onnx", help="direkt nach ONNX exportieren")
    train.add_argument(
        "--tiny",
        action="store_true",
        help="kleine Aufloesung und wenige Schritte (Rauchtest)",
    )
    train.set_defaults(handler=_train)

    export = sub.add_parser("export", help="Gewichte nach ONNX exportieren")
    export.add_argument("--out", required=True)
    export.add_argument("--state", help="torch-Gewichte (.pt); fehlt sie, wird ein "
                                        "untrainiertes Netz exportiert")
    export.add_argument("--side", type=int, default=None)
    export.set_defaults(handler=_export)

    return parser


def _detect(args) -> int:
    from .pipeline import detect_position

    started = time.time()
    image = load_rgb(args.image)

    detection = detect_position(
        image,
        size_hint=args.size,
        komi=args.komi,
        backend=args.backend,
        weights=args.weights,
    )

    payload = detection.position.to_json()
    print(payload)

    if args.json:
        with open(args.json, "w", encoding="utf-8") as handle:
            handle.write(payload + "\n")

    if args.debug:
        from .debug import overlay

        save_rgb(overlay(detection), args.debug)

    print(
        "goteach-vision: Brett {size}x{size}, Backend {backend}, "
        "Gitterguete {grid:.2f}, Konfidenz min {low:.2f} / mittel {mean:.2f}, "
        "{stones} Steine, {seconds:.2f}s".format(
            size=detection.position.size,
            backend=detection.backend,
            grid=detection.geometry.confidence,
            low=detection.min_confidence,
            mean=detection.mean_confidence,
            stones=detection.position.stones(),
            seconds=time.time() - started,
        ),
        file=sys.stderr,
    )

    if detection.min_confidence < args.min_confidence:
        print(
            f"goteach-vision: Konfidenz {detection.min_confidence:.2f} unter "
            f"{args.min_confidence:.2f} — Ergebnis nicht belastbar",
            file=sys.stderr,
        )

        return EXIT_UNSURE

    return EXIT_OK


def _render(args) -> int:
    from .render.photo import render_photo
    from .render.screenshot import render_screenshot

    rng = np.random.default_rng(args.seed)

    if args.domain == "photo":
        sample = render_photo(rng, args.size)
    else:
        sample = render_screenshot(rng, args.size, palette=args.palette)

    save_rgb(sample.image, args.out)

    if args.labels:
        from .contract import Position

        with open(args.labels, "w", encoding="utf-8") as handle:
            handle.write(Position.from_labels(sample.labels).to_json() + "\n")

    print(
        f"goteach-vision: {args.domain} {args.size}x{args.size} -> {args.out} "
        f"({sample.image.shape[1]}x{sample.image.shape[0]} px)",
        file=sys.stderr,
    )

    return EXIT_OK


def _train(args) -> int:
    from .train import train

    steps = 20 if args.tiny else args.steps
    side = 128 if args.tiny else None

    kwargs = dict(
        steps=steps,
        batch=args.batch,
        learning_rate=args.lr,
        photo_ratio=args.photo_ratio,
        output=args.out,
    )

    if side:
        kwargs["side"] = side
        kwargs["validate_every"] = steps
        kwargs["validation_size"] = 4

    model = train(**kwargs)

    if args.onnx:
        from .export import export

        export(model, args.onnx, side=side or 512)

    return EXIT_OK


def _export(args) -> int:
    from .export import export, load_weights
    from .geometry import CANONICAL_SIDE
    from .unet import UNet

    model = UNet()

    if args.state:
        load_weights(model, args.state)

    export(model, args.out, side=args.side or CANONICAL_SIDE)
    print(f"goteach-vision: ONNX -> {args.out}", file=sys.stderr)

    return EXIT_OK


if __name__ == "__main__":
    raise SystemExit(main())
