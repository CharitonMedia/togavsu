# Matter Clock sample module image. Test scaffolding; not part of the release.
# Build from the repository root: docker build -f examples/matter-clock/Dockerfile .
FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/matter-clock ./examples/matter-clock/cmd/matter-clock

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/matter-clock /matter-clock
EXPOSE 8080
ENTRYPOINT ["/matter-clock", "serve"]
