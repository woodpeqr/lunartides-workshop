FROM golang:alpine AS build
WORKDIR /app
COPY go.mod go.sum vendor/ ./
RUN true
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -o /moontide ./cmd/moontide/

FROM alpine:3.21
COPY --from=build /moontide /moontide
EXPOSE 8080
CMD ["/moontide"]
