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

## Logging — seeing which pod called which pod

Both services log structured JSON to stdout (`log/slog`), each line tagged
with `pod` (from the `HOSTNAME` env var, which k8s sets to the pod name).

A request ID is generated in `gateway-service` for each incoming HTTP call
(or reused if the caller already sent an `X-Request-Id` header), then passed
to `todo-service` as gRPC metadata (`x-request-id`). Both services log that
same ID, so you can grep one ID and see the full path of a request:

```json
{"msg":"http request received","pod":"gateway-service-5d6c7-xyz12","request_id":"0622...","method":"POST","path":"/todos"}
{"msg":"todo created","pod":"todo-service-7f8b9-abcde","id":"1","title":"test logging"}
{"msg":"rpc handled","pod":"todo-service-7f8b9-abcde","request_id":"0622...","method":"/todo.TodoService/CreateTodo","caller_addr":"127.0.0.1:45008","duration":34895}
{"msg":"http request completed","pod":"gateway-service-5d6c7-xyz12","request_id":"0622...","status":201,"duration":4442490}
```

In k8s, tail both at once with:

```bash
kubectl logs -f -l app=gateway-service --prefix=true
kubectl logs -f -l app=todo-service --prefix=true
# or, with stern/kubectl-tail installed, just:
stern 'gateway-service|todo-service'
```

**This is app-level logging.** Once Istio or Linkerd is actually in the
mesh, you get a second, independent layer for free — the sidecar (Envoy for
Istio, linkerd2-proxy for Linkerd) logs every request it proxies, including
source pod, destination pod, response code, and latency, without any app
changes. That's worth comparing side-by-side with these logs:
- Istio: enable access logs via `meshConfig.accessLogFile: /dev/stdout`, then
  `kubectl logs <pod> -c istio-proxy`.
- Linkerd: `linkerd viz tap deploy/gateway-service` shows live request traffic
  in/out, and `linkerd viz stat` gives success-rate/latency per service.

For actual distributed tracing (a single trace spanning both pods, viewable
as a waterfall) rather than just correlated log lines, both meshes can emit
spans to Jaeger/Zipkin/Tempo automatically — that's a good next step once
you want to go beyond grepping a request ID.

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
