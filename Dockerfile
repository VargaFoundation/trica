# trica server image: a static binary on distroless, running as non-root, with the
# web UI built from web/ and embedded (build tag webui, see docs/adr/0018).
# Build: docker build --build-arg VERSION=$(git describe --tags --always) -t trica .
#
# A second target, `ops`, adds the PostgreSQL 17 client tools that
# `trica backup` and `trica restore` run (ADR 0021). It is listed before the
# final stage so that a plain `docker build .` still yields the server image:
#   docker build --target ops -t trica-ops .

# Stage 1: the UI. Vite writes into internal/web/dist, where go:embed reads it.
FROM node:22.23.3-bookworm-slim AS ui
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# Stage 2: the binary.
FROM golang:1.26.9-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/internal/web/dist ./internal/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -tags webui -ldflags "-s -w -X main.version=${VERSION}" -o /out/trica ./cmd/trica

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
