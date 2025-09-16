# --- build stage ---
FROM golang:1.24-alpine AS builder
ENV GOTOOLCHAIN=auto
WORKDIR /app

# leverage module cache
COPY go.mod go.sum ./
RUN go mod download

# copy source
COPY server ./server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /app/ai-thunderdome ./server

# --- run stage (tiny & secure) ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /app/ai-thunderdome /app/ai-thunderdome
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/ai-thunderdome"]
