# Marketplace service image (PRD 10.6). Deployed separately from the firm's Compose
# stack: docker build -f marketplace/Dockerfile -t togavsu-marketplace .
# Mount a config file at /etc/togavsu-marketplace/config.json (see config.example.json),
# the operator token as the Docker secret marketplace_operator_token, and a volume at
# /var/lib/togavsu-marketplace for listings and packages.
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/marketplace ./marketplace/cmd/marketplace

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && install -d -o 65532 -g 65532 -m 700 /var/lib/togavsu-marketplace
COPY --from=build /out/marketplace /usr/local/bin/marketplace
COPY marketplace/config.example.json /etc/togavsu-marketplace/config.json
LABEL togavsu.role=marketplace
USER 65532:65532
EXPOSE 8090
VOLUME ["/var/lib/togavsu-marketplace"]
ENTRYPOINT ["/usr/local/bin/marketplace"]
CMD ["-config", "/etc/togavsu-marketplace/config.json"]
