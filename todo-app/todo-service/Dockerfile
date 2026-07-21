FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/todo-service .

FROM gcr.io/distroless/static-debian12
COPY --from=builder /out/todo-service /todo-service
EXPOSE 50051
ENTRYPOINT ["/todo-service"]