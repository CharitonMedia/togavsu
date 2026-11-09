# Supervisor image. The product core image is deploy/core.Dockerfile. cmd/core
# accepts bootstrap configuration and does not start HTTP or migrations.

FROM golang:1.22-bookworm AS supervisor-build
WORKDIR /src
COPY cmd/supervisor ./cmd/supervisor
COPY internal/supervisor ./internal/supervisor
RUN printf 'module github.com/CharitonMedia/togavsu\n\ngo 1.22\n' > go.mod \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/supervisor ./cmd/supervisor

# ponytail: the supervisor image runs as root so it can use the Docker socket,
# ceiling: one root container, upgrade: a socket proxy that exposes only the
# engine calls this process makes, trigger: phase 7 release hardening.
FROM postgres:17
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends openssh-client \
    && rm -rf /var/lib/apt/lists/* \
    && command -v sftp
COPY --from=supervisor-build /out/supervisor /usr/local/bin/supervisor
ENTRYPOINT ["/usr/local/bin/supervisor"]
CMD ["serve"]
