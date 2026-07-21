package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	pb "todo-service/proto"
)

// server implements pb.TodoServiceServer with a simple in-memory store.
type server struct {
	pb.UnimplementedTodoServiceServer

	mu     sync.Mutex
	todos  map[string]*pb.Todo
	nextID int
}

func newServer() *server {
	return &server{
		todos: make(map[string]*pb.Todo),
	}
}

func (s *server) CreateTodo(ctx context.Context, req *pb.CreateTodoRequest) (*pb.Todo, error) {
	if req.GetTitle() == "" {
		return nil, status.Error(codes.InvalidArgument, "title must not be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	id := fmt.Sprintf("%d", s.nextID)
	todo := &pb.Todo{
		Id:        id,
		Title:     req.GetTitle(),
		Completed: false,
		CreatedAt: time.Now().Unix(),
	}
	s.todos[id] = todo

	log.Printf("created todo id=%s title=%q", id, todo.Title)
	return todo, nil
}

func (s *server) ListTodos(ctx context.Context, req *pb.ListTodosRequest) (*pb.ListTodosResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	resp := &pb.ListTodosResponse{}
	for _, t := range s.todos {
		resp.Todos = append(resp.Todos, t)
	}
	return resp, nil
}

func (s *server) GetTodo(ctx context.Context, req *pb.GetTodoRequest) (*pb.Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	todo, ok := s.todos[req.GetId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "todo %q not found", req.GetId())
	}
	return todo, nil
}

func (s *server) UpdateTodo(ctx context.Context, req *pb.UpdateTodoRequest) (*pb.Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	todo, ok := s.todos[req.GetId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "todo %q not found", req.GetId())
	}
	if req.GetTitle() != "" {
		todo.Title = req.GetTitle()
	}
	return todo, nil
}

func (s *server) DeleteTodo(ctx context.Context, req *pb.DeleteTodoRequest) (*pb.DeleteTodoResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.todos[req.GetId()]; !ok {
		return nil, status.Errorf(codes.NotFound, "todo %q not found", req.GetId())
	}
	delete(s.todos, req.GetId())
	return &pb.DeleteTodoResponse{Success: true}, nil
}

func (s *server) ToggleTodo(ctx context.Context, req *pb.ToggleTodoRequest) (*pb.Todo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	todo, ok := s.todos[req.GetId()]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "todo %q not found", req.GetId())
	}
	todo.Completed = !todo.Completed
	return todo, nil
}

func main() {
	port := ":50051"
	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterTodoServiceServer(grpcServer, newServer())

	// gRPC health checking - useful for k8s readiness/liveness probes
	// and service mesh health checks (Istio/Linkerd).
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	// reflection makes it easy to poke the service with grpcurl for testing.
	reflection.Register(grpcServer)

	log.Printf("todo-service listening on %s", port)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
