# Product core image. Fails closed until the contracts assignment adds go.mod
# and cmd/core. This Dockerfile does not invent a setup wizard.
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
LABEL togavsu.role=core
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/core"]
