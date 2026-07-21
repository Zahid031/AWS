# --- build stage ---
FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/gateway-service .

# --- runtime stage ---
FROM gcr.io/distroless/static-debian12

COPY --from=builder /out/gateway-service /gateway-service

ENV TODO_SERVICE_ADDR=todo-service:50051
EXPOSE 8080
ENTRYPOINT ["/gateway-service"]
