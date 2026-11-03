# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/brain ./cmd/brain \
    && mkdir -p /out/data \
    && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/brain /brain
COPY --from=build --chown=65532:65532 /out/data /data

ENV BRAIN_ADDR=0.0.0.0:8080 \
    BRAIN_DB=/data/brain.db

EXPOSE 8080
USER 65532:65532

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/brain", "healthcheck"]

ENTRYPOINT ["/brain"]
CMD ["serve"]
