# trica server image: a static binary on distroless, running as non-root.
# Build: docker build --build-arg VERSION=$(git describe --tags --always) -t trica .
#
# A second target, `ops`, adds the PostgreSQL 17 client tools that
# `trica backup` and `trica restore` run (ADR 0021). It is listed before the
# final stage so that a plain `docker build .` still yields the server image:
#   docker build --target ops -t trica-ops .
FROM golang:1.26.9-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/trica ./cmd/trica

FROM postgres:17-alpine AS ops
COPY --from=build /out/trica /usr/local/bin/trica
RUN mkdir /backups && chown 65532:65532 /backups
USER 65532:65532
VOLUME /backups
ENTRYPOINT ["/usr/local/bin/trica"]
CMD ["help"]

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/trica /usr/local/bin/trica
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/trica"]
CMD ["server"]
