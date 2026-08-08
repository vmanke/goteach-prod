"""Stufe 1: Brett finden und entzerren.

Klassisches CV statt eines zweiten Netzes — und zwar aus einem konkreten
Grund: Die beiden Linienbueschel eines Go-Gitters bestimmen die Homographie
vollstaendig, und bei achsparallelen Screenshots degeneriert sie rechnerisch
zu Skalierung plus Zuschnitt. Der Screenshot-Fall braucht deshalb keinen
eigenen Codepfad; er faellt aus derselben Mathematik.

Ablauf:

1. Kanten und Liniensegmente, aufgeteilt in zwei Winkelbueschel.
2. Je Bueschel die Gitterlinien clustern; die aeussersten liefern
   Kandidaten fuer das Brettviereck.
3. Fuer jedes Kandidatenviereck probeweise entzerren und die Brettgroesse
   ueber *Projektionsprofile* bestimmen. Dieser Umweg ueber die Pixel ist
   der Grund, warum dicht besetzte Bretter funktionieren: einzelne
   Gitterlinien sind dort von Steinen verdeckt, aber die Summe entlang einer
   ganzen Linie bleibt sichtbar, und das periodische Muster aller Linien
   erst recht.
4. Feinjustage der Lage per Kreuzkorrelation, dann endgueltige Entzerrung.
"""

from __future__ import annotations

from dataclasses import dataclass

import cv2
import numpy as np

from .contract import BOARD_SIZES
from .grid import DEFAULT_MARGIN, line_positions

# Groesse, auf die zur Liniensuche heruntergerechnet wird; die Homographie
# wird anschliessend auf die Originalaufloesung zurueckskaliert.
WORK_MAX = 1200

# Kanonische Kantenlaenge des entzerrten Bildes.
CANONICAL_SIDE = 512

# Die Rahmensuche entzerrt pro Kandidat probeweise — und zwar in derselben
# Aufloesung wie das Endergebnis. Eine kleinere Suchaufloesung waere billiger,
# wuerde aber beim Verkleinern genau die ein Pixel duennen Gitterlinien
# wegaliasen, auf die es hier ankommt.
SCORE_SIDE = CANONICAL_SIDE

# So viele Rahmenkandidaten je Brettgroesse gehen in die Feinjustage.
FRAMES_PER_SIZE = 3


@dataclass
class Geometry:
    """Ergebnis der Entzerrung."""

    homography: np.ndarray  # Originalbild -> kanonisches Quadrat
    size: int
    side: int
    margin: float
    corners: np.ndarray  # (4, 2) im Originalbild: TL, TR, BR, BL
    confidence: float
    warped: np.ndarray  # (side, side, 3) uint8


class BoardNotFound(RuntimeError):
    """Das Gitter liess sich nicht zuverlaessig bestimmen."""


def detect(
    rgb: np.ndarray,
    size_hint: int | None = None,
    side: int = CANONICAL_SIDE,
    margin: float = DEFAULT_MARGIN,
) -> Geometry:
    """Findet das Brett in ``rgb`` und liefert die entzerrte Ansicht."""
    work, scale = _downscale(rgb)
    segments = _segments(work)

    if len(segments) < 8:
        raise BoardNotFound(f"zu wenige Liniensegmente ({len(segments)})")

    groups = _split_by_angle(segments)

    if groups is None:
        raise BoardNotFound("keine zwei Linienbueschel gefunden")

    clusters = [_cluster_lines(g, work.shape) for g in groups]

    for axis, cl in enumerate(clusters):
        if len(cl) < 2:
            raise BoardNotFound(f"Bueschel {axis} hat nur {len(cl)} Linien")

    response = _line_response(work)
    frames = _best_frame(response, clusters, size_hint, margin)

    if not frames:
        raise BoardNotFound("kein konsistentes Gitter")

    # Die Brettgroesse erst *nach* der Feinjustage entscheiden: ein grober
    # Rahmen sitzt leicht daneben, und ein leicht verschobenes Gitter der
    # falschen Groesse kann dabei besser aussehen als das richtige. Nach der
    # Justage rastet nur die richtige Groesse sauber ein.
    best = None

    for coarse, board_size, _ in frames:
        matrix = _refine(
            response, _rescale(coarse, SCORE_SIDE, side), board_size, side, margin
        )
        warped = cv2.warpPerspective(response, matrix, (side, side))
        profiles = [_profile(warped, axis, margin) for axis in range(2)]
        score = _comb_score(profiles, board_size, side, margin)

        if best is None or score > best[2]:
            best = (matrix, board_size, score)

    matrix, board_size, score = best

    # Zurueck auf Originalkoordinaten: die Arbeitskopie war skaliert.
    homography = matrix @ np.diag([scale, scale, 1.0])
    corners = _corners_from(homography, board_size, side, margin)

    warped = cv2.warpPerspective(rgb, homography, (side, side), flags=cv2.INTER_AREA)

    return Geometry(
        homography=homography,
        size=board_size,
        side=side,
        margin=margin,
        corners=corners,
        confidence=float(score),
        warped=warped,
    )


# --------------------------------------------------------------------------
# Vorverarbeitung
# --------------------------------------------------------------------------


def _downscale(rgb: np.ndarray) -> tuple[np.ndarray, float]:
    h, w = rgb.shape[:2]
    longest = max(h, w)

    if longest <= WORK_MAX:
        return rgb, 1.0

    scale = WORK_MAX / longest

    return cv2.resize(rgb, (int(w * scale), int(h * scale)), cv2.INTER_AREA), scale


def _line_response(rgb: np.ndarray) -> np.ndarray:
    """Antwort auf duenne Strukturen beliebiger Polaritaet, hell = Linie.

    Morphologischer Top-/Blackhat statt einer Differenz zum weichgezeichneten
    Bild. Der Unterschied ist entscheidend: Eine Weichzeichnungsdifferenz
    antwortet auch auf *Stufen*, also auf Steinraender — und die liegen eine
    halbe Gitterteilung neben den Linien. Das erzeugt ein Scheinsignal bei
    doppelter Brettgroesse. Ein Kernel knapp oberhalb der Linienbreite
    antwortet dagegen nur auf schmale Ruecken und laesst Stufen bei null.

    Beide Polaritaeten, weil Dunkelmodus-Oberflaechen *helle* Gitterlinien
    auf dunklem Brett zeichnen.
    """
    gray = cv2.cvtColor(rgb, cv2.COLOR_RGB2GRAY).astype(np.float32)
    kernel = cv2.getStructuringElement(cv2.MORPH_RECT, (7, 7))

    dark = cv2.morphologyEx(gray, cv2.MORPH_BLACKHAT, kernel)
    bright = cv2.morphologyEx(gray, cv2.MORPH_TOPHAT, kernel)

    return np.maximum(dark, bright)


def _segments(rgb: np.ndarray) -> np.ndarray:
    """Liniensegmente ueber Canny und probabilistische Hough-Transformation."""
    gray = cv2.cvtColor(rgb, cv2.COLOR_RGB2GRAY)
    gray = cv2.GaussianBlur(gray, (0, 0), 1.0)

    # Otsu liefert die obere Canny-Schwelle unabhaengig von Helligkeit und
    # Kontrast — wichtig, weil Screenshots und Fotos hier weit auseinander
    # liegen.
    upper, _ = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY + cv2.THRESH_OTSU)
    edges = cv2.Canny(gray, max(10.0, 0.4 * upper), max(30.0, upper))

    # Auf dicht besetzten Brettern verdecken Steine die Gitterlinien; die
    # Segmente muessen deshalb kurz sein duerfen und ueber einen Stein hinweg
    # verbunden werden.
    shortest = min(rgb.shape[:2])
    lines = cv2.HoughLinesP(
        edges,
        rho=1,
        theta=np.pi / 720,
        threshold=max(25, int(0.04 * shortest)),
        minLineLength=max(15, int(0.09 * shortest)),
        maxLineGap=max(6, int(0.06 * shortest)),
    )

    if lines is None:
        return np.empty((0, 4), dtype=np.float64)

    return lines.reshape(-1, 4).astype(np.float64)


def _split_by_angle(segments: np.ndarray):
    """Teilt Segmente in die zwei dominanten Richtungen (mod 180 Grad)."""
    dx = segments[:, 2] - segments[:, 0]
    dy = segments[:, 3] - segments[:, 1]
    length = np.hypot(dx, dy)
    angle = np.mod(np.arctan2(dy, dx), np.pi)

    bins = 180
    hist = np.zeros(bins)
    idx = np.minimum((angle / np.pi * bins).astype(int), bins - 1)
    np.add.at(hist, idx, length)

    # Zirkulaer glaetten, damit ein Peak nicht auf zwei Bins zerfaellt.
    kernel = np.array([0.25, 0.5, 1.0, 0.5, 0.25])
    hist = np.convolve(np.concatenate([hist[-2:], hist, hist[:2]]), kernel, "same")[2:-2]

    first = int(hist.argmax())

    # Zweiten Peak nur ausserhalb von +-25 Grad um den ersten suchen.
    offsets = np.arange(bins) - first
    circular = np.minimum(np.abs(offsets), bins - np.abs(offsets))
    masked = np.where(circular > 25, hist, -1.0)
    second = int(masked.argmax())

    if masked[second] <= 0:
        return None

    peaks = np.array([first, second]) * np.pi / bins
    diff = np.abs(angle[:, None] - peaks[None, :])
    diff = np.minimum(diff, np.pi - diff)
    assign = diff.argmin(axis=1)

    groups = [segments[assign == k] for k in range(2)]

    if min(len(g) for g in groups) < 2:
        return None

    # Bueschel mit den staerker senkrechten Linien zuerst: dessen Lage wird
    # zur kanonischen x-Achse, was die Brettorientierung bei achsparallelen
    # Bildern unveraendert laesst.
    if _verticality(groups[1]) > _verticality(groups[0]):
        groups.reverse()

    return groups


def _verticality(segments: np.ndarray) -> float:
    dx = segments[:, 2] - segments[:, 0]
    dy = segments[:, 3] - segments[:, 1]

    return float(np.mean(np.abs(dy) / (np.hypot(dx, dy) + 1e-9)))


def _cluster_lines(segments: np.ndarray, shape) -> list[dict]:
    """Fasst Segmente derselben Gitterlinie zusammen.

    Ordnungsparameter ist die Lage entlang der gemeinsamen Normalen; sie
    waechst monoton entlang des Bueschels, auch unter Perspektive.
    """
    h, w = shape[:2]
    centre = np.array([w / 2.0, h / 2.0])

    lines = _to_lines(segments)
    normal = _reference_normal(segments)

    # Normalenrichtung vereinheitlichen, damit die Lage ein Vorzeichen hat.
    flip = np.sign(lines[:, :2] @ normal)
    flip[flip == 0] = 1.0
    lines = lines * flip[:, None]

    # Vorzeichen beachten: a*cx + b*cy + c ist der Abstand des *Punktes* von
    # der Geraden und waechst gegenlaeufig zu deren Lage.
    dist = -(lines[:, 0] * centre[0] + lines[:, 1] * centre[1] + lines[:, 2])
    weight = np.hypot(segments[:, 2] - segments[:, 0], segments[:, 3] - segments[:, 1])

    # Canny liefert pro gezeichneter Linie *zwei* Kanten, die wieder
    # verschmelzen muessen. Die Toleranz richtet sich deshalb nach der
    # kleinstmoeglichen Gitterteilung: Spannweite geteilt durch das groesste
    # Brett. Perzentile statt Extremwerte, damit ein Bildrand die Spannweite
    # nicht aufblaeht.
    if len(dist) >= 4:
        span = float(np.percentile(dist, 99) - np.percentile(dist, 1))
    else:
        span = float(np.ptp(dist))

    tol = max(2.0, 0.35 * span / (max(BOARD_SIZES) - 1))
    clusters: list[dict] = []

    for i in np.argsort(dist):
        if clusters and abs(dist[i] - clusters[-1]["dist"]) <= tol:
            c = clusters[-1]
            total = c["weight"] + weight[i]
            c["dist"] = (c["dist"] * c["weight"] + dist[i] * weight[i]) / total
            c["weight"] = total
            c["members"].append(i)
        else:
            clusters.append(
                {"dist": float(dist[i]), "weight": float(weight[i]), "members": [i]}
            )

    # Schwache Cluster verwerfen: Steinkonturen erzeugen kurze Segmente,
    # echte Gitterlinien tragen viel Laenge.
    if clusters:
        strongest = max(c["weight"] for c in clusters)
        clusters = [c for c in clusters if c["weight"] >= 0.12 * strongest]

    # Linien direkt am Bildrand sind Artefakte des Zuschnitts, nie Brett.
    # Sie muessen weg, bevor die Rahmensuche laeuft: sonst verdraengen sie
    # als aeusserste Kandidaten die tatsaechliche Gitterlinie.
    extent = w if abs(normal[0]) >= abs(normal[1]) else h
    clusters = [c for c in clusters if abs(c["dist"]) < 0.47 * extent]

    for c in clusters:
        c["line"] = _fit_line(segments[c["members"]], weight[c["members"]], normal)

    return clusters


def _fit_line(members: np.ndarray, weight: np.ndarray, normal: np.ndarray):
    """Totale kleinste Quadrate durch alle Endpunkte des Clusters.

    Ein gewichtetes Mittel der einzelnen Geradenvektoren waere billiger, aber
    zu ungenau: schon ein halbes Grad Restdrehung verschmiert das spaetere
    Projektionsprofil ueber mehrere Pixel und laesst die Gitterspitzen
    verschwinden. Die Ecken des Bretts entstehen aus genau diesen Geraden,
    ihre Richtung muss also stimmen.
    """
    points = np.concatenate([members[:, :2], members[:, 2:]], axis=0)
    weights = np.concatenate([weight, weight])
    weights = weights / (weights.sum() + 1e-12)

    centre = (points * weights[:, None]).sum(axis=0)
    centred = points - centre
    cov = (centred * weights[:, None]).T @ centred

    # Kleinster Eigenwert: Richtung quer zur Geraden, also die Normale.
    _, vectors = np.linalg.eigh(cov)
    direction = vectors[:, 1]
    line_normal = np.array([-direction[1], direction[0]])

    if line_normal @ normal < 0:
        line_normal = -line_normal

    return np.array(
        [line_normal[0], line_normal[1], -float(line_normal @ centre)],
        dtype=np.float64,
    )


def _to_lines(segments: np.ndarray) -> np.ndarray:
    """Segmente als homogene Geraden [a, b, c] mit a^2 + b^2 = 1."""
    p1 = np.concatenate([segments[:, :2], np.ones((len(segments), 1))], axis=1)
    p2 = np.concatenate([segments[:, 2:], np.ones((len(segments), 1))], axis=1)
    lines = np.cross(p1, p2)

    return lines / (np.linalg.norm(lines[:, :2], axis=1, keepdims=True) + 1e-12)


def _reference_normal(segments: np.ndarray) -> np.ndarray:
    """Gemeinsame Normalenrichtung des Bueschels, in +x bzw. +y zeigend."""
    dx = (segments[:, 2] - segments[:, 0]).sum()
    dy = (segments[:, 3] - segments[:, 1]).sum()
    direction = np.array([dx, dy], dtype=np.float64)
    direction /= np.linalg.norm(direction) + 1e-12

    normal = np.array([-direction[1], direction[0]])

    if abs(normal[0]) >= abs(normal[1]):
        return normal if normal[0] > 0 else -normal

    return normal if normal[1] > 0 else -normal


# --------------------------------------------------------------------------
# Rahmen und Brettgroesse
# --------------------------------------------------------------------------


def _best_frame(response, clusters, size_hint, margin):
    """Waehlt Kandidatenviereck und Brettgroesse ueber Projektionsprofile.

    Der aeusserste Cluster eines Bueschels ist nicht zwingend die aeusserste
    Gitterlinie — Brettkante, Tischkante oder Bildrand koennen davor liegen.
    Deshalb werden je Seite die zwei aeussersten Kandidaten durchprobiert.
    """
    sizes = (size_hint,) if size_hint else BOARD_SIZES
    candidates = []

    for axis in range(2):
        ordered = sorted(clusters[axis], key=lambda c: c["dist"])
        depth = min(2, len(ordered) - 1)
        candidates.append((ordered[:depth], ordered[-depth:]))

    # Mehrere Rahmen *je* Brettgroesse; die Entscheidung faellt der Aufrufer
    # nach der Feinjustage. Nur den grob besten zu behalten waere zu
    # voreilig: die Justage kann einen knapp zweitplatzierten Rahmen deutlich
    # verbessern, den grob besten dagegen gar nicht.
    found: dict[int, list] = {}

    for a_low in candidates[0][0]:
        for a_high in candidates[0][1]:
            for b_low in candidates[1][0]:
                for b_high in candidates[1][1]:
                    quad = _quad(a_low, a_high, b_low, b_high)

                    if quad is None:
                        continue

                    matrix = _homography(quad, SCORE_SIDE, margin)

                    if matrix is None:
                        continue

                    warped = cv2.warpPerspective(
                        response, matrix, (SCORE_SIDE, SCORE_SIDE)
                    )
                    profiles = [_profile(warped, axis, margin) for axis in range(2)]

                    for board_size in sizes:
                        score = _comb_score(profiles, board_size, SCORE_SIDE, margin)
                        found.setdefault(board_size, []).append(
                            (matrix, board_size, score)
                        )

    frames = []

    for board_size, entries in found.items():
        entries.sort(key=lambda f: f[2], reverse=True)
        frames.extend(f for f in entries[:FRAMES_PER_SIZE] if f[2] > 0.0)

    return frames


def _quad(a_low, a_high, b_low, b_high):
    """Vier Ecken als Schnittpunkte der aeussersten Linien beider Bueschel."""
    if a_low is a_high or b_low is b_high:
        return None

    pts = []
    order = ((a_low, b_low), (a_high, b_low), (a_high, b_high), (a_low, b_high))

    for a, b in order:
        p = np.cross(a["line"], b["line"])

        if abs(p[2]) < 1e-9:
            return None

        pts.append(p[:2] / p[2])

    quad = np.array(pts, dtype=np.float64)

    # Entartete Vierecke (Linien fast parallel) verwerfen.
    side = np.linalg.norm(quad[1] - quad[0]) * np.linalg.norm(quad[3] - quad[0])

    if not np.isfinite(quad).all() or side < 1.0:
        return None

    return quad


def _homography(quad: np.ndarray, side: int, margin: float):
    inner = side * margin
    outer = side - inner
    dst = np.array(
        [[inner, inner], [outer, inner], [outer, outer], [inner, outer]],
        dtype=np.float32,
    )

    matrix = cv2.getPerspectiveTransform(quad.astype(np.float32), dst)

    if matrix is None or not np.isfinite(matrix).all():
        return None

    return matrix.astype(np.float64)


def _profile(warped: np.ndarray, axis: int, margin: float) -> np.ndarray:
    """Mittlere Linienantwort je Spalte (axis 0) bzw. Zeile (axis 1).

    Gemittelt wird nur ueber das Brettinnere, damit Tischkanten ausserhalb
    des Gitters nicht mitzaehlen.
    """
    side = warped.shape[0]
    lo = int(side * margin)
    hi = side - lo

    band = warped[lo:hi, :] if axis == 0 else warped[:, lo:hi]
    profile = band.mean(axis=0 if axis == 0 else 1)

    return profile.astype(np.float64)


def _comb_score(profiles, board_size: int, side: int, margin: float) -> float:
    """Kontrast zwischen erwarteten Linien und deren Zwischenraeumen.

    Eine blosse Korrelation mit einem Linienkamm trennt die Brettgroessen
    schlecht: der Kamm einer zu groben Hypothese liegt teilweise auf echten
    Linien und bekommt dafuer Punkte. Entscheidend ist die Gegenprobe — auf
    dem *richtigen* Gitter sind die Mittelpunkte zwischen zwei Linien leer.
    Liegt dort Signal, war die Hypothese zu grob.
    """
    positions = line_positions(board_size, side, margin)
    midpoints = (positions[:-1] + positions[1:]) / 2.0
    scores = []

    for profile in profiles:
        detrended = _detrend(profile, side, margin)
        spread = detrended.std()

        if spread < 1e-9:
            return 0.0

        on_line = np.interp(positions, np.arange(side), detrended).mean()
        between = np.interp(midpoints, np.arange(side), detrended).mean()

        scores.append(float((on_line - between) / spread))

    # Die schwaechere der beiden Achsen zaehlt: ein Gitter, das nur in einer
    # Richtung passt, ist keins.
    return float(np.min(scores))


def _detrend(profile: np.ndarray, side: int, margin: float) -> np.ndarray:
    """Entfernt die breite Struktur (Steine, Brettrand) aus dem Profil.

    Die Glaettungsbreite liegt oberhalb der groebsten Gitterteilung
    (Spannweite/8 bei 9x9), damit keine echte Periodizitaet wegfaellt.
    """
    span = side * (1.0 - 2.0 * margin)
    smooth = cv2.GaussianBlur(
        profile.reshape(1, -1).astype(np.float32), (0, 0), span / 6.0
    ).ravel()

    return profile - smooth.astype(np.float64)


# --------------------------------------------------------------------------
# Feinjustage
# --------------------------------------------------------------------------


def _rescale(matrix: np.ndarray, old_side: int, new_side: int) -> np.ndarray:
    """Rechnet eine Homographie auf eine andere kanonische Kantenlaenge um."""
    factor = new_side / old_side

    return np.diag([factor, factor, 1.0]) @ matrix


def _refine(response, matrix, board_size, side, margin, rounds: int = 2):
    """Korrigiert Lage und Massstab des Gitters ueber die Profilspitzen.

    Der grobe Rahmen stammt aus den aeussersten erkannten Linien und sitzt
    typisch ein bis zwei Pixel daneben. Hier wird je Achse eine 1D-Affine
    (Massstab und Versatz) an die tatsaechlichen Spitzen gefittet.

    Die Justage ist bewusst misstrauisch gegen sich selbst: sie akzeptiert
    nur schwache Korrekturen und behaelt das Ergebnis nur, wenn der
    Gitterkontrast dadurch *steigt*. Ohne diese Bremse rastet der Fit auf
    einer verschobenen Gitterordnung ein und verschlechtert einen bereits
    guten Rahmen um eine ganze Gitterteilung.
    """
    expected = line_positions(board_size, side, margin)
    pitch = expected[1] - expected[0]

    best = matrix
    best_score = _score_at(response, matrix, board_size, side, margin)
    current = matrix

    for _ in range(rounds):
        warped = cv2.warpPerspective(response, current, (side, side))
        correction = np.eye(3)
        moved = False

        for axis in range(2):
            profile = _profile(warped, axis, margin)
            found = _peaks_near(profile, expected, 0.25 * pitch)
            valid = np.isfinite(found)

            if valid.sum() < max(3, board_size // 2):
                continue

            # found ~ scale * expected + offset
            scale, offset = np.polyfit(expected[valid], found[valid], 1)

            if not np.isfinite([scale, offset]).all():
                continue

            # Der grobe Rahmen ist bereits nah dran; alles andere waere ein
            # Einrasten auf der falschen Ordnung.
            if abs(scale - 1.0) > 0.05 or abs(offset) > 0.5 * pitch:
                continue

            correction[axis, axis] = 1.0 / scale
            correction[axis, 2] = -offset / scale
            moved = True

        if not moved:
            break

        current = correction @ current
        score = _score_at(response, current, board_size, side, margin)

        if score > best_score:
            best, best_score = current, score

    return best


def _score_at(response, matrix, board_size, side, margin) -> float:
    """Gitterkontrast einer konkreten Homographie."""
    warped = cv2.warpPerspective(response, matrix, (side, side))
    profiles = [_profile(warped, axis, margin) for axis in range(2)]

    return _comb_score(profiles, board_size, side, margin)


def _peaks_near(profile: np.ndarray, expected: np.ndarray, tolerance: float):
    """Schwerpunkt der Profilspitze nahe jeder erwarteten Linie.

    Fenster ohne echte Spitze liefern NaN statt eines beliebigen
    Schwerpunkts — sonst zieht Rauschen den Fit mit.
    """
    out = np.full(expected.shape, np.nan)
    side = len(profile)
    floor = float(np.median(profile))
    threshold = floor + 0.5 * float(profile.std())

    for k, position in enumerate(expected):
        lo = max(0, int(np.floor(position - tolerance)))
        hi = min(side, int(np.ceil(position + tolerance)) + 1)

        if hi - lo < 3 or profile[lo:hi].max() < threshold:
            continue

        window = np.clip(profile[lo:hi] - floor, 0.0, None)
        total = window.sum()

        if total <= 1e-9:
            continue

        out[k] = (np.arange(lo, hi) * window).sum() / total

    return out


def _corners_from(matrix, board_size, side, margin) -> np.ndarray:
    """Die vier aeussersten Schnittpunkte zurueck im Originalbild."""
    positions = line_positions(board_size, side, margin)
    first, last = positions[0], positions[-1]
    canonical = np.array(
        [[first, first], [last, first], [last, last], [first, last]],
        dtype=np.float64,
    )

    inverse = np.linalg.inv(matrix)
    hom = np.concatenate([canonical, np.ones((4, 1))], axis=1) @ inverse.T

    return hom[:, :2] / hom[:, 2:3]
