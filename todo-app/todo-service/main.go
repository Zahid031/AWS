package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"

	pb "todo-service/proto"
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
	slog.String("service", "todo-service"),
	slog.String("pod", podName),
)

// requestIDInterceptor logs every incoming RPC with the caller's request ID
// (propagated via gRPC metadata from gateway-service), the calling peer
// address, method name, duration, and result — this is what lets you trace
// "which pod called which pod" without needing the mesh's own logs.
func requestIDInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	if info.FullMethod == "/grpc.health.v1.Health/Check" || info.FullMethod == "/grpc.health.v1.Health/Watch" {
		return handler(ctx, req)
	}	
	start := time.Now()

	requestID := "unknown"
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-request-id"); len(vals) > 0 {
			requestID = vals[0]
		}
	}

	callerAddr := "unknown"
	if p, ok := peer.FromContext(ctx); ok {
		callerAddr = p.Addr.String()
	}

	resp, err := handler(ctx, req)

	attrs := []any{
		slog.String("request_id", requestID),
		slog.String("method", info.FullMethod),
		slog.String("caller_addr", callerAddr),
		slog.Duration("duration", time.Since(start)),
	}
	if err != nil {
		logger.Error("rpc failed", append(attrs, slog.String("error", err.Error()))...)
	} else {
		logger.Info("rpc handled", attrs...)
	}

	return resp, err
}

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

	logger.Info("todo created", slog.String("id", id), slog.String("title", todo.Title))
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

	grpcServer := grpc.NewServer(grpc.UnaryInterceptor(requestIDInterceptor))
	pb.RegisterTodoServiceServer(grpcServer, newServer())

	// gRPC health checking - useful for k8s readiness/liveness probes
	// and service mesh health checks (Istio/Linkerd).
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthServer)

	// reflection makes it easy to poke the service with grpcurl for testing.
	reflection.Register(grpcServer)

	logger.Info("todo-service listening", slog.String("port", port))
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
