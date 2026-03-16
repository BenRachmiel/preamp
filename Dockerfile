FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /preamp ./cmd/preamp/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /preamp /preamp
EXPOSE 4533
ENTRYPOINT ["/preamp"]
