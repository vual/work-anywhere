# The builder runs natively on the build host and cross-compiles for each
# target platform, so multi-arch images do not need QEMU for the Go build.
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS build

ARG VERSION=dev
ARG REVISION=unknown
ARG BUILD_TIME=unknown
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go test ./... && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
      -ldflags="-s -w -X github.com/vual/work-anywhere/relay/internal/buildinfo.Version=${VERSION} -X github.com/vual/work-anywhere/relay/internal/buildinfo.Revision=${REVISION} -X github.com/vual/work-anywhere/relay/internal/buildinfo.BuildTime=${BUILD_TIME}" \
      -o /out/relay ./cmd/relay

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/relay /relay
USER nonroot:nonroot
ENTRYPOINT ["/relay"]
CMD ["serve"]
