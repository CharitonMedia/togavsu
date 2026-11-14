# Product core image. The binary migrates, seeds reference rows and serves HTTP on
# :8080; templates and the API contract are embedded. "core check" only validates
# the four bootstrap values. The go.mod and cmd/core checks still fail the build if
# those files are removed.
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY . .
# TOGAVSU_VERSION is the release version /api/v1/health reports (internal/core.Version).
ARG TOGAVSU_VERSION=dev
RUN test -f go.mod \
    && test -d cmd/core \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/CharitonMedia/togavsu/internal/core.Version=$TOGAVSU_VERSION" -o /out/core ./cmd/core

# LawCraft (PRD 8.20) at the pinned commit, built by web/editor/build.sh: wordcraft-cli,
# which the core runs for editor runs, merge fields, conversions and Doc Tender's Word
# reads, and the browser editor the edit page serves at /editor/. build.sh refuses a
# checkout that is not exactly internal/domain.LawCraftCommit. Only the files build.sh
# reads are copied, so a Go change does not rebuild LawCraft.
FROM rust:1.99.1-bookworm AS editor
ARG LAWCRAFT_REPO=https://github.com/CharitonMedia/LawCraft
ARG TRUNK_VERSION=0.21.14
RUN apt-get update \
    && apt-get install -y --no-install-recommends patch \
    && rm -rf /var/lib/apt/lists/* \
    && rustup target add wasm32-unknown-unknown \
    && cargo install trunk --version "$TRUNK_VERSION" --locked
COPY internal/domain/defaults.go /src/internal/domain/defaults.go
RUN pin=$(sed -n 's/^[[:space:]]*LawCraftCommit[[:space:]]*=[[:space:]]*"\([0-9a-f]\{40\}\)".*/\1/p' /src/internal/domain/defaults.go) \
    && test -n "$pin" \
    && git clone --filter=blob:none "$LAWCRAFT_REPO" /lawcraft \
    && git -C /lawcraft checkout --detach "$pin"
COPY web/editor /src/web/editor
RUN sh /src/web/editor/build.sh /lawcraft /out \
    && /out/wordcraft-cli --help >/dev/null \
    && test -f /out/editor/index.html

FROM debian:bookworm-slim
# Tesseract is Doc Tender's second reader of scanned pages and photos, and ffmpeg reads
# audio and video the native readers can't (PRD 8.21). The engine and its English data
# are pinned: the data decides how a page reads (Alpine's eng.traineddata, not the
# engine version, left the synthetic scan's page 1 at agreement 0.692), so a changed
# file fails the build here instead of silently moving real pages to review.
ARG TESSERACT_VERSION=5.3.0-2
ARG TESSERACT_ENG_VERSION=1:4.1.0-2
ARG TESSERACT_ENG_SHA256=7d4322bd2a7749724879683fc3912cb542f19906c83bcc1a52132556427170b2
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
       "tesseract-ocr=$TESSERACT_VERSION" "libtesseract5=$TESSERACT_VERSION" "tesseract-ocr-eng=$TESSERACT_ENG_VERSION" \
    && rm -rf /var/lib/apt/lists/* \
    && echo "$TESSERACT_ENG_SHA256  /usr/share/tesseract-ocr/5/tessdata/eng.traineddata" | sha256sum -c -
# The core's data volume (core.DataDir): a new named volume takes this directory's
# owner, so the non-root core can write upload staging and the local-disk file store.
RUN install -d -o 65532 -g 65532 -m 0700 /var/lib/togavsu-core
COPY --from=build /out/core /usr/local/bin/core
# core.WordcraftCLI and core.EditorDist.
COPY --from=editor /out/wordcraft-cli /usr/local/bin/wordcraft-cli
COPY --from=editor /out/editor /usr/share/togavsu/editor
COPY deploy/core/entrypoint.sh /usr/local/bin/core-entrypoint
RUN chmod 755 /usr/local/bin/core-entrypoint
# The supervisor compares the togavsu.version label of the running and the advertised
# core; the same build argument is the version compiled into the binary above.
ARG TOGAVSU_VERSION=dev
LABEL togavsu.role=core togavsu.version=$TOGAVSU_VERSION
USER 65532:65532
EXPOSE 8080
# The supervisor swaps in a core only after Docker reports this check healthy, and waits
# for it after it restarts the core for a restore (PRD 6). --start-interval needs Docker 25+.
HEALTHCHECK --interval=10s --timeout=4s --start-period=120s --start-interval=1s --retries=3 \
    CMD ["/usr/local/bin/core", "healthcheck"]
ENTRYPOINT ["/bin/sh", "/usr/local/bin/core-entrypoint"]
