FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o nanocontainer ./cmd/nanocontainer

FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=builder /app/nanocontainer /usr/local/bin/nanocontainer

RUN mkdir -p /var/lib/nanocontainer/images /var/lib/nanocontainer/containers /var/lib/nanocontainer/volumes /var/log/nanocontainer

EXPOSE 8080

ENTRYPOINT ["nanocontainer"]
CMD ["serve", "--addr", ":8080"]
