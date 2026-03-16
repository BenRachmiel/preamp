FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /preamp ./cmd/preamp/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /scrape-art ./cmd/scrape-art/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /preamp /preamp
COPY --from=builder /scrape-art /scrape-art
EXPOSE 4533
ENTRYPOINT ["/preamp"]
