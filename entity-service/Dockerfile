# entity-service — student-owned REST service (CRUD over entities).
# Multi-stage: build static binary on go 1.26, ship on distroless.
FROM golang:1.26-alpine AS build
WORKDIR /src

# Deps are vendored (vendor/) so the build needs no network — works behind a
# TLS-intercepting corporate proxy where go mod download would fail.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -o /out/entity-service ./cmd/entity-service

# Pre-create a writable store dir owned by the distroless nonroot user (65532).
# distroless has no shell to mkdir at runtime, so it must exist at build time.
RUN mkdir -p /data && chown 65532:65532 /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/entity-service /entity-service
COPY --from=build --chown=65532:65532 /data /data
EXPOSE 8081
USER nonroot:nonroot
ENTRYPOINT ["/entity-service"]
