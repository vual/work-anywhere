FROM golang:1.26.5-bookworm AS build

ARG VERSION=dev
ARG REVISION=unknown
ARG BUILD_TIME=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test ./... && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
      -ldflags="-s -w -X github.com/vual/work-anywhere/relay/internal/buildinfo.Version=${VERSION} -X github.com/vual/work-anywhere/relay/internal/buildinfo.Revision=${REVISION} -X github.com/vual/work-anywhere/relay/internal/buildinfo.BuildTime=${BUILD_TIME}" \
      -o /out/relay ./cmd/relay

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/relay /relay
USER nonroot:nonroot
ENTRYPOINT ["/relay"]
CMD ["serve"]
