# Product core image. Builds the contracts bootstrap binary. The process
# accepts its four environment values and does not start HTTP or migrations.
# The go.mod and cmd/core checks still fail the build if those files are removed.
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY . .
RUN test -f go.mod \
    && test -d cmd/core \
    && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/core ./cmd/core

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/core /usr/local/bin/core
COPY deploy/core/entrypoint.sh /usr/local/bin/core-entrypoint
RUN chmod 755 /usr/local/bin/core-entrypoint
LABEL togavsu.role=core
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/bin/sh", "/usr/local/bin/core-entrypoint"]
