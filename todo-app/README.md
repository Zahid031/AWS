# Todo App — gRPC Backend (for Istio/Linkerd testing)

Two Go services, talking to each other over gRPC — good minimal setup for
exercising mesh features (mTLS, retries, load balancing, observability).

```
gateway-service (HTTP :8080)  --gRPC-->  todo-service (gRPC :50051)
      ^                                        |
      |                                  in-memory store
   frontend (later)
```

- **todo-service**: gRPC server, in-memory todo store (Create/List/Get/Update/Delete/Toggle).
  Includes a gRPC health check endpoint (used by k8s probes and by meshes for outlier detection).
- **gateway-service**: plain HTTP/REST server for the frontend to call. Translates
  REST calls into gRPC calls to `todo-service`. This gateway→todo-service hop is
  the "backend-to-backend gRPC call" the mesh will sit in front of.

## Build

Each service is its own Go module.

```bash
cd todo-service
go mod tidy
go build -o todo-service .

cd ../gateway-service
go mod tidy
go build -o gateway-service .
```

`go mod tidy` needs normal internet access to fetch the grpc-go and
protobuf modules — it wasn't run against a real proxy here, so go.sum
isn't included; the first `go mod tidy` will generate it.

## Run locally (no k8s yet)

```bash
# terminal 1
./todo-service/todo-service
# listens on :50051

# terminal 2
TODO_SERVICE_ADDR=localhost:50051 ./gateway-service/gateway-service
# listens on :8080
```

Try it:

```bash
curl -X POST localhost:8080/todos -d '{"title":"buy milk"}'
curl localhost:8080/todos
curl -X POST localhost:8080/todos/1/toggle
curl localhost:8080/todos/1
curl -X DELETE localhost:8080/todos/1
```

## REST API (gateway-service)

| Method | Path                | Body                | Description        |
|--------|---------------------|----------------------|---------------------|
| GET    | /todos               | –                    | list todos          |
| POST   | /todos               | `{"title": "..."}`   | create todo          |
| GET    | /todos/{id}           | –                    | get one todo         |
| PUT    | /todos/{id}           | `{"title": "..."}`   | update title         |
| DELETE | /todos/{id}           | –                    | delete todo          |
| POST   | /todos/{id}/toggle    | –                    | toggle completed     |
| GET    | /healthz              | –                    | liveness check       |

## Regenerating the proto stubs

If you change `proto/todo.proto`, regenerate with:

```bash
protoc --go_out=todo-service/proto --go_opt=paths=source_relative \
  --go-grpc_out=todo-service/proto --go-grpc_opt=paths=source_relative \
  proto/todo.proto

# gateway-service needs the same generated files (it only uses the client stub)
cp todo-service/proto/*.go gateway-service/proto/
```

(Requires `protoc`, `protoc-gen-go`, and `protoc-gen-go-grpc` on your PATH.)

## Notes for k8s + service mesh testing

- `gateway-service` looks up `todo-service` via the env var `TODO_SERVICE_ADDR`,
  defaulting to `todo-service:50051` — matches a k8s Service named
  `todo-service` in the same namespace, which is what you'll want once these
  are deployed and you point Istio/Linkerd's sidecar at the gRPC port.
- `todo-service` exposes a standard gRPC health service
  (`grpc.health.v1.Health`) — useful for k8s `grpc` readiness/liveness probes
  and for the mesh's own health-based routing.
- Both are plaintext (no TLS) by design — that's exactly what you want before
  the mesh is in the picture; the mesh should be the one adding mTLS, not the app.
- Not containerized yet — say the word if you want Dockerfiles + k8s manifests
  (Deployment/Service for each, gRPC port named appropriately for Istio's
  protocol sniffing) next.
