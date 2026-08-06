# goteach mit echter KataGo-Engine (CPU/Eigen) als ein Container:
# Go-Server + KataGo-Binary + neuronales Netz. Der Server startet KataGo
# pro /analyze-Anfrage als Kindprozess (Analysis Engine, JSON über
# stdin/stdout) — gesteuert über KATAGO_PATH/KATAGO_MODEL/KATAGO_CONFIG.

# ---- Stufe 1: Go-Server bauen ---------------------------------------------
FROM golang:1.22 AS gobuild
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/goteach-server .

# ---- Stufe 2: KataGo-Release und Netz laden -------------------------------
FROM ubuntu:22.04 AS katago
ARG KATAGO_VERSION=1.17.1
# eigenavx2 setzt AVX2 voraus (übliche x64-Server); für ältere CPUs
# per Build-Arg KATAGO_FLAVOR=eigen bauen.
ARG KATAGO_FLAVOR=eigenavx2
# Erwartete SHA256-Prüfsumme für katago-v1.17.1-eigenavx2-linux-x64.zip.
# Bei einem Versions- oder Flavor-Wechsel muss dieser Wert aktualisiert werden.
ARG KATAGO_ZIP_SHA256=234bf7866bc26f37baaeed60dc358b821bafc8e73e9bc50cb2d2a1cf51502d44
# Transformer-Netz aus demselben Release: klein (36 MB), aber stärker
# pro Visit als die stärksten b18-Netze des Hauptlaufs.
ARG KATAGO_NET=b10c384h6nbttflrs.bin.gz

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl unzip \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /katago

RUN curl -fSL -o katago.zip "https://github.com/lightvector/KataGo/releases/download/v${KATAGO_VERSION}/katago-v${KATAGO_VERSION}-${KATAGO_FLAVOR}-linux-x64.zip" \
 && echo "${KATAGO_ZIP_SHA256}  katago.zip" | sha256sum -c - \
 && unzip -q katago.zip katago \
 && rm katago.zip

# Das Release-Binary ist ein AppImage: einmal beim Bauen entpacken,
# damit nicht jeder Engine-Start neu extrahiert (und kein FUSE nötig ist).
# squashfs-root/AppRun setzt LD_LIBRARY_PATH auf die gebündelten Libs.
RUN ./katago --appimage-extract >/dev/null \
 && ./squashfs-root/AppRun version

RUN curl -fSL -o net.bin.gz "https://github.com/lightvector/KataGo/releases/download/v${KATAGO_VERSION}/${KATAGO_NET}"

# ---- Stufe 3: Laufzeit-Image ----------------------------------------------
FROM ubuntu:22.04

# curl nur für den Docker-Healthcheck.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --system --create-home --home-dir /app --shell /usr/sbin/nologin goteach

WORKDIR /app

COPY --from=gobuild /out/goteach-server /app/goteach-server
COPY --from=katago /katago/squashfs-root /app/katago
COPY --from=katago /katago/net.bin.gz /app/net.bin.gz
COPY analysis.cfg /app/analysis.cfg

# analysis.cfg schreibt Engine-Logs nach ./analysis_logs (relativ zum CWD).
RUN mkdir -p /app/analysis_logs && chown -R goteach:goteach /app

ENV KATAGO_PATH=/app/katago/AppRun \
    KATAGO_MODEL=/app/net.bin.gz \
    KATAGO_CONFIG=/app/analysis.cfg \
    PORT=8080

USER goteach
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s \
  CMD curl -fsS "http://localhost:${PORT}/healthz" || exit 1

CMD ["/app/goteach-server"]
