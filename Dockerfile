# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH VERSION=dev
RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/edt ./cmd/edt

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/edt /usr/local/bin/edt
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/edt"]
CMD ["--help"]
