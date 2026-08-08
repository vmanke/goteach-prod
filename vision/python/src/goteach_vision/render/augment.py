"""Augmentierung der gerenderten Bretter.

Die perspektivische Verzerrung wird auf Bild *und* Eckpunkte angewandt, damit
die Ground Truth der Homographie erhalten bleibt. Photometrische Stoerungen
folgen der Realitaet von PNG-Quellen: Unschaerfe, Sensorrauschen,
Farbtemperatur, Verdeckungen und gelegentlich Palettenquantisierung (PNG-8).
JPEG-Artefakte fehlen bewusst — das Eingabeformat ist PNG.
"""

from __future__ import annotations

import cv2
import numpy as np

from . import Sample


def perspective(rng: np.random.Generator, sample: Sample, strength: float) -> Sample:
    """Verzerrt das Bild perspektivisch und fuehrt die Ecken mit."""
    if strength <= 0:
        return sample

    h, w = sample.image.shape[:2]
    src = np.array([[0, 0], [w, 0], [w, h], [0, h]], dtype=np.float32)
    jitter = rng.uniform(-strength, strength, size=(4, 2)) * np.array([w, h])
    dst = src + jitter.astype(np.float32)

    # Auf den Ursprung schieben, damit nichts abgeschnitten wird.
    dst -= dst.min(axis=0)
    out_w = int(np.ceil(dst[:, 0].max())) + 1
    out_h = int(np.ceil(dst[:, 1].max())) + 1

    matrix = cv2.getPerspectiveTransform(src, dst)
    border = tuple(int(v) for v in sample.image.reshape(-1, 3).mean(axis=0))

    image = cv2.warpPerspective(
        sample.image,
        matrix,
        (out_w, out_h),
        flags=cv2.INTER_LINEAR,
        borderMode=cv2.BORDER_CONSTANT,
        borderValue=border,
    )

    return Sample(
        image=image,
        labels=sample.labels,
        corners=apply_homography(matrix, sample.corners),
        size=sample.size,
        domain=sample.domain,
    )


def apply_homography(matrix: np.ndarray, points: np.ndarray) -> np.ndarray:
    """Wendet eine 3x3-Homographie auf (N, 2)-Punkte an."""
    pts = np.asarray(points, dtype=np.float64)
    hom = np.concatenate([pts, np.ones((pts.shape[0], 1))], axis=1)
    out = hom @ np.asarray(matrix, dtype=np.float64).T

    return out[:, :2] / out[:, 2:3]


def photometric(rng: np.random.Generator, sample: Sample, strength: float) -> Sample:
    """Unschaerfe, Rauschen, Farbtemperatur, Verdeckung, Quantisierung."""
    img = sample.image.astype(np.float32)

    if strength > 0 and rng.random() < 0.6:
        img = _blur(rng, img, strength)

    if strength > 0 and rng.random() < 0.5:
        img = _colour_temperature(rng, img, strength)

    if strength > 0 and rng.random() < 0.35:
        img = _occluders(rng, img, sample, strength)

    if strength > 0:
        img = img + rng.normal(0.0, 2.0 + 9.0 * strength, size=img.shape)

    out = np.clip(img, 0, 255).astype(np.uint8)

    # PNG-8: reale Screenshots werden oft auf eine Palette reduziert.
    if rng.random() < 0.15:
        out = _quantise(rng, out)

    return Sample(
        image=out,
        labels=sample.labels,
        corners=sample.corners,
        size=sample.size,
        domain=sample.domain,
    )


def _blur(rng, img, strength):
    if rng.random() < 0.5:
        sigma = float(rng.uniform(0.4, 0.5 + 2.2 * strength))

        return cv2.GaussianBlur(img, (0, 0), sigma)

    # Bewegungsunschaerfe: Linienkern in zufaelliger Richtung.
    length = int(rng.integers(3, max(4, int(4 + 14 * strength))))
    kernel = np.zeros((length, length), dtype=np.float32)
    kernel[length // 2, :] = 1.0
    angle = float(rng.uniform(0, 180))
    rot = cv2.getRotationMatrix2D((length / 2 - 0.5, length / 2 - 0.5), angle, 1.0)
    kernel = cv2.warpAffine(kernel, rot, (length, length))
    kernel /= kernel.sum() + 1e-6

    return cv2.filter2D(img, -1, kernel)


def _colour_temperature(rng, img, strength):
    gain = 1.0 + rng.uniform(-0.16, 0.16, size=3) * (0.4 + strength)

    return img * gain[None, None, :].astype(np.float32)


def _occluders(rng, img, sample, strength):
    """Haende, Schalen, Bildrandobjekte als weiche dunkle Formen."""
    h, w = img.shape[:2]
    out = img.copy()

    for _ in range(int(rng.integers(1, 3))):
        mask = np.zeros((h, w), dtype=np.float32)
        cx = float(rng.uniform(0, w))
        cy = float(rng.uniform(0, h))
        ax = float(rng.uniform(0.05, 0.16 + 0.12 * strength)) * w
        ay = float(rng.uniform(0.05, 0.16 + 0.12 * strength)) * h

        cv2.ellipse(
            mask,
            (int(cx), int(cy)),
            (int(ax), int(ay)),
            float(rng.uniform(0, 180)),
            0,
            360,
            1.0,
            -1,
        )
        mask = cv2.GaussianBlur(mask, (0, 0), max(2.0, 0.02 * w))

        tint = rng.uniform(30, 200, size=3).astype(np.float32)
        out = out * (1 - mask[:, :, None]) + tint[None, None, :] * mask[:, :, None]

    return out


def _quantise(rng, img):
    """Reduziert auf eine adaptive Palette, wie ein PNG-8-Export."""
    colours = int(rng.integers(24, 128))
    flat = img.reshape(-1, 3).astype(np.float32)

    # k-Means auf einer Stichprobe reicht und bleibt schnell.
    sample = flat[:: max(1, flat.shape[0] // 8000)]
    criteria = (cv2.TERM_CRITERIA_EPS + cv2.TERM_CRITERIA_MAX_ITER, 8, 1.0)
    _, _, centres = cv2.kmeans(
        sample, colours, None, criteria, 1, cv2.KMEANS_PP_CENTERS
    )

    dist = ((flat[:, None, :] - centres[None, :, :]) ** 2).sum(axis=2)
    out = centres[dist.argmin(axis=1)]

    return np.clip(out.reshape(img.shape), 0, 255).astype(np.uint8)
