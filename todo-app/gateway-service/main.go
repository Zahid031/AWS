package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "gateway-service/proto"
)

// gatewayServer holds the gRPC client used to talk to todo-service.
type gatewayServer struct {
	todoClient pb.TodoServiceClient
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
		resp, err := g.todoClient.ListTodos(ctx, &pb.ListTodosRequest{})
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
		todo, err := g.todoClient.CreateTodo(ctx, &pb.CreateTodoRequest{Title: body.Title})
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
		todo, err := g.todoClient.ToggleTodo(ctx, &pb.ToggleTodoRequest{Id: id})
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
		todo, err := g.todoClient.GetTodo(ctx, &pb.GetTodoRequest{Id: id})
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
		todo, err := g.todoClient.UpdateTodo(ctx, &pb.UpdateTodoRequest{Id: id, Title: body.Title})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toDTO(todo))

	case http.MethodDelete:
		_, err := g.todoClient.DeleteTodo(ctx, &pb.DeleteTodoRequest{Id: id})
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

	// Simple CORS so a local frontend dev server can call this directly.
	handler := corsMiddleware(mux)

	port := ":8080"
	log.Printf("gateway-service listening on %s, forwarding to todo-service at %s", port, todoServiceAddr)
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
