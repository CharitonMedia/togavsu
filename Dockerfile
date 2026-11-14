# Supervisor image. The product core image is deploy/core.Dockerfile. cmd/core
# accepts bootstrap configuration and does not start HTTP or migrations.

FROM golang:1.22-bookworm AS supervisor-build
WORKDIR /src
COPY cmd/supervisor ./cmd/supervisor
COPY internal/supervisor ./internal/supervisor
COPY internal/ledgerseal ./internal/ledgerseal
RUN printf 'module github.com/CharitonMedia/togavsu\n\ngo 1.22\n' > go.mod \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/supervisor ./cmd/supervisor

# ponytail: the supervisor image runs as root so it can use the Docker socket,
# ceiling: one root container, upgrade: a socket proxy that exposes only the
# engine calls this process makes, trigger: phase 7 release hardening.
FROM postgres:17
USER root
# apt.postgresql.org (in the postgres base image) sometimes serves an index mid-sync;
# both apt-get update calls retry the index fetch before giving up.
RUN for i in 1 2 3 4 5; do apt-get update && break; [ "$i" -lt 5 ] || exit 1; sleep $((i * 10)); done \
    && apt-get install -y --no-install-recommends openssh-client \
    && rm -rf /var/lib/apt/lists/* \
    && command -v sftp
# cosign verifies the release signature before an update (PRD 6, AC-016). The release
# binary is pinned by version and SHA-256 per architecture.
ARG TARGETARCH
ARG COSIGN_VERSION=v2.4.1
ARG COSIGN_SHA256_AMD64=8b24b946dd5809c6bd93de08033bcf6bc0ed7d336b7785787c080f574b89249b
ARG COSIGN_SHA256_ARM64=3b2e2e3854d0356c45fe6607047526ccd04742d20bd44afb5be91fa2a6e7cb4a
RUN arch="${TARGETARCH:-$(dpkg --print-architecture)}" \
    && case "$arch" in \
         amd64) sum="$COSIGN_SHA256_AMD64" ;; \
         arm64) sum="$COSIGN_SHA256_ARM64" ;; \
         *) echo "no pinned cosign for $arch" >&2; exit 1 ;; \
       esac \
    && for i in 1 2 3 4 5; do apt-get update && break; [ "$i" -lt 5 ] || exit 1; sleep $((i * 10)); done \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && curl -fsSL -o /usr/local/bin/cosign "https://github.com/sigstore/cosign/releases/download/${COSIGN_VERSION}/cosign-linux-${arch}" \
    && echo "$sum  /usr/local/bin/cosign" | sha256sum -c - \
    && chmod 755 /usr/local/bin/cosign \
    && apt-get purge -y curl && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* \
    && cosign version >/dev/null
# The release public key (compose.yaml COSIGN_KEY). The private key is kept off GitHub;
# release signatures are made there and are not in the public transparency log.
COPY deploy/release/cosign.pub /usr/share/togavsu/cosign.pub
RUN chmod 644 /usr/share/togavsu/cosign.pub
COPY --from=supervisor-build /out/supervisor /usr/local/bin/supervisor
ENTRYPOINT ["/usr/local/bin/supervisor"]
CMD ["serve"]
