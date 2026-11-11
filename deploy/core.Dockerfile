# Product core image. The binary migrates, seeds reference rows and serves HTTP on
# :8080; templates and the API contract are embedded. "core check" only validates
# the four bootstrap values. The go.mod and cmd/core checks still fail the build if
# those files are removed.
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY . .
RUN test -f go.mod \
    && test -d cmd/core \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/core ./cmd/core

FROM debian:bookworm-slim
# Tesseract is Doc Tender's second reader of scanned pages and photos, and ffmpeg reads
# audio and video the native readers can't (PRD 8.21).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tesseract-ocr tesseract-ocr-eng ffmpeg \
    && rm -rf /var/lib/apt/lists/*
# The core's data volume (core.DataDir): a new named volume takes this directory's
# owner, so the non-root core can write upload staging and the local-disk file store.
RUN install -d -o 65532 -g 65532 -m 0700 /var/lib/togavsu-core
COPY --from=build /out/core /usr/local/bin/core
COPY deploy/core/entrypoint.sh /usr/local/bin/core-entrypoint
RUN chmod 755 /usr/local/bin/core-entrypoint
LABEL togavsu.role=core
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/bin/sh", "/usr/local/bin/core-entrypoint"]
