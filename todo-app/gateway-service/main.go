package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "gateway-service/proto"
)

// podName identifies which pod produced a given log line — this is what lets
// you tell "pod A called pod B" apart once there's more than one replica.
var podName = func() string {
	if h := os.Getenv("HOSTNAME"); h != "" {
		return h // k8s sets HOSTNAME to the pod name by default
	}
	h, _ := os.Hostname()
	return h
}()

var logger = slog.New(slog.NewJSONHandler(os.Stdout, nil)).With(
	slog.String("service", "gateway-service"),
	slog.String("pod", podName),
)

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

// gatewayServer holds the gRPC client used to talk to todo-service.
type gatewayServer struct {
	todoClient pb.TodoServiceClient
}

type ctxKey string

const requestIDKey ctxKey = "request_id"

// requestIDFrom pulls the request ID out of a context, or "unknown" if absent.
func requestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return "unknown"
}

// grpcCtx attaches the request ID onto an outgoing gRPC context, so
// todo-service can log the same ID gateway-service used for the HTTP call —
// that's the thread that ties "pod A called pod B" together across the hop.
func grpcCtx(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-request-id", requestIDFrom(ctx))
}

// requestIDMiddleware assigns (or forwards) a request ID and logs every HTTP
// call in and out with method, path, status, and duration.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()

		reqID := r.Header.Get("X-Request-Id")
		if reqID == "" {
			reqID = newRequestID()
		}
		w.Header().Set("X-Request-Id", reqID)

		ctx := context.WithValue(r.Context(), requestIDKey, reqID)
		r = r.WithContext(ctx)

		logger.Info("http request received",
			slog.String("request_id", reqID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
		)

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		logger.Info("http request completed",
			slog.String("request_id", reqID),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

// statusWriter captures the status code so it can be logged after the handler runs.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

type todoDTO struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
	CreatedAt int64  `json:"created_at"`
}

func toDTO(t *pb.Todo) todoDTO {
	return todoDTO{
		ID:        t.GetId(),
		Title:     t.GetTitle(),
		Completed: t.GetCompleted(),
		CreatedAt: t.GetCreatedAt(),
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	st, ok := status.FromError(err)
	code := http.StatusInternalServerError
	if ok {
		switch st.Code().String() {
		case "NotFound":
			code = http.StatusNotFound
		case "InvalidArgument":
			code = http.StatusBadRequest
		}
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

// handleTodos routes /todos (GET list, POST create).
func (g *gatewayServer) handleTodos(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	switch r.Method {
	case http.MethodGet:
		resp, err := g.todoClient.ListTodos(grpcCtx(ctx), &pb.ListTodosRequest{})
		if err != nil {
			writeError(w, err)
			return
		}
		out := make([]todoDTO, 0, len(resp.GetTodos()))
		for _, t := range resp.GetTodos() {
			out = append(out, toDTO(t))
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		var body struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		todo, err := g.todoClient.CreateTodo(grpcCtx(ctx), &pb.CreateTodoRequest{Title: body.Title})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toDTO(todo))

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleTodoByID routes /todos/{id} (GET, PUT, DELETE) and /todos/{id}/toggle (POST).
func (g *gatewayServer) handleTodoByID(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	path := strings.TrimPrefix(r.URL.Path, "/todos/")
	if strings.HasSuffix(path, "/toggle") {
		id := strings.TrimSuffix(path, "/toggle")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		todo, err := g.todoClient.ToggleTodo(grpcCtx(ctx), &pb.ToggleTodoRequest{Id: id})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toDTO(todo))
		return
	}

	id := path
	switch r.Method {
	case http.MethodGet:
		todo, err := g.todoClient.GetTodo(grpcCtx(ctx), &pb.GetTodoRequest{Id: id})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toDTO(todo))

	case http.MethodPut:
		var body struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		todo, err := g.todoClient.UpdateTodo(grpcCtx(ctx), &pb.UpdateTodoRequest{Id: id, Title: body.Title})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toDTO(todo))

	case http.MethodDelete:
		_, err := g.todoClient.DeleteTodo(grpcCtx(ctx), &pb.DeleteTodoRequest{Id: id})
		if err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func main() {
	todoServiceAddr := os.Getenv("TODO_SERVICE_ADDR")
	if todoServiceAddr == "" {
		// Default assumes a k8s Service named "todo-service" in the same namespace.
		todoServiceAddr = "todo-service:50051"
	}

	conn, err := grpc.NewClient(todoServiceAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to todo-service at %s: %v", todoServiceAddr, err)
	}
	defer conn.Close()

	gw := &gatewayServer{todoClient: pb.NewTodoServiceClient(conn)}

	mux := http.NewServeMux()
	mux.HandleFunc("/todos", gw.handleTodos)
	mux.HandleFunc("/todos/", gw.handleTodoByID)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Simple CORS so a local frontend dev server can call this directly,
	// then the request-ID/logging middleware wraps everything.
	handler := requestIDMiddleware(corsMiddleware(mux))

	port := ":8080"
	logger.Info("gateway-service listening", slog.String("port", port), slog.String("todo_service_addr", todoServiceAddr))
	log.Fatal(http.ListenAndServe(port, handler))
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
