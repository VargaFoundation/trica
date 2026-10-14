# trica server image: a static binary on distroless, running as non-root.
# Build: docker build --build-arg VERSION=$(git describe --tags --always) -t trica .
FROM golang:1.26.9-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/trica ./cmd/trica

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/trica /usr/local/bin/trica
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/trica"]
CMD ["server"]
