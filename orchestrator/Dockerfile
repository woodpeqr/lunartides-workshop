FROM golang:1.24-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /moontide ./cmd/moontide/

FROM alpine:3.21
COPY --from=build /moontide /moontide
EXPOSE 8080
CMD ["/moontide"]
