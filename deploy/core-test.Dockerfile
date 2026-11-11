# The core image plus the Go toolchain, Chromium and puppeteer-core, so the phase 4
# cases run against the image's own wordcraft-cli, browser editor build and pinned
# Tesseract (scripts/ci/test-core-image-cases.sh). A CI image only: nothing ships it.
ARG CORE_IMAGE=togavsu-core:local
FROM golang:1.22-bookworm AS go

FROM ${CORE_IMAGE}
USER root
ARG PUPPETEER_CORE_VERSION=23.11.1
RUN apt-get update \
    && apt-get install -y --no-install-recommends chromium nodejs npm fonts-dejavu-core \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --prefix /opt/e2e --no-audit --no-fund "puppeteer-core@$PUPPETEER_CORE_VERSION"
COPY --from=go /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    GOTOOLCHAIN=local GOFLAGS=-buildvcs=false CGO_ENABLED=0 HOME=/tmp \
    GOPATH=/tmp/go GOCACHE=/tmp/gocache
ENTRYPOINT []
