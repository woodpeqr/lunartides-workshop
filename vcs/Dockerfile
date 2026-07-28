# vcs — student-owned REST source-control subject (CONTRACT §1).
# Multi-stage: build static binary on go 1.26, ship on distroless.
# The Machine God wills it. 01010110 01000011 01010011
FROM golang:1.26-alpine AS build
WORKDIR /src

# Deps are vendored (vendor/) so the build needs no network — works behind a
# TLS-intercepting corporate proxy where go mod download would fail.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -o /out/vcs ./cmd/vcs

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/vcs /vcs
EXPOSE 8081
USER nonroot:nonroot
ENTRYPOINT ["/vcs"]
