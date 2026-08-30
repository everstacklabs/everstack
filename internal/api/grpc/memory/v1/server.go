package v1

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/memory"
	memorypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/memory/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/memory/v1/memorypbconnect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Server implements the MemoryService Connect/gRPC service.
// The server can be created without a backend — handlers return a clear
// "memory backend not configured" error until SetBackend is called.
type Server struct {
	ctx          context.Context
	store        memory.VectorStore
	embedder     memory.EmbedderInterface
	defaultModel string
	defaultDim   int
	DB           *sqlx.DB
	reqLogger    *memory.RequestLogger
}

// CreateServer creates a memory server without a backend.
// Call SetBackend to wire in the vector store and embedder later.
func CreateServer(ctx context.Context) *Server {
	return &Server{ctx: ctx}
}

// CreateServerWithDeps creates a memory server with all dependencies.
func CreateServerWithDeps(ctx context.Context, store memory.VectorStore, embedder memory.EmbedderInterface, model string, dim int) *Server {
	return &Server{
		ctx:          ctx,
		store:        store,
		embedder:     embedder,
		defaultModel: model,
		defaultDim:   dim,
	}
}

// SetBackend wires the vector store and embedder after creation.
func (s *Server) SetBackend(store memory.VectorStore, embedder memory.EmbedderInterface, model string, dim int) {
	s.store = store
	s.embedder = embedder
	s.defaultModel = model
	s.defaultDim = dim
}

// SetDB wires a PostgreSQL connection for request logging and analytics.
func (s *Server) SetDB(db *sqlx.DB) {
	s.DB = db
	s.reqLogger = memory.NewRequestLogger(db)
}

// Ready returns true if a memory backend is configured.
func (s *Server) Ready() bool {
	return s.store != nil && s.embedder != nil
}

// RegisterConnectServer registers the ConnectRPC handler.
func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	return memorypbconnect.NewMemoryServiceHandler(s, connect.WithInterceptors(interceptors...))
}

// FileDescriptor returns the proto file descriptor.
func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return memorypb.File_everstack_memory_v1_memory_service_proto
}

// AppName returns the service name.
func (s *Server) AppName() string {
	return memorypbconnect.MemoryServiceName
}

// MethodPrefix returns the method prefix.
func (s *Server) MethodPrefix() string {
	return memorypbconnect.MemoryServiceName
}

// RegisterGateway wires REST endpoints under /v1 via grpc-gateway.
func (s *Server) RegisterGateway(_ context.Context, mux *gwruntime.ServeMux, _ string, _ []grpc.DialOption) error {
	if err := mux.HandlePath("POST", "/v1/memory/collections", s.handleCreateCollection); err != nil {
		return err
	}
	if err := mux.HandlePath("GET", "/v1/memory/collections", s.handleListCollections); err != nil {
		return err
	}
	if err := mux.HandlePath("GET", "/v1/memory/collections/{name}", s.handleGetCollection); err != nil {
		return err
	}
	if err := mux.HandlePath("DELETE", "/v1/memory/collections/{name}", s.handleDeleteCollection); err != nil {
		return err
	}
	if err := mux.HandlePath("POST", "/v1/memory/collections/{name}/documents", s.handleAddDocuments); err != nil {
		return err
	}
	if err := mux.HandlePath("DELETE", "/v1/memory/collections/{name}/documents/{document_id}", s.handleDeleteDocument); err != nil {
		return err
	}
	if err := mux.HandlePath("POST", "/v1/memory/collections/{name}/query", s.handleQueryCollection); err != nil {
		return err
	}
	if err := mux.HandlePath("POST", "/v1/memory/analytics", s.handleGetMemoryAnalytics); err != nil {
		return err
	}
	if err := mux.HandlePath("POST", "/v1/memory/setup", s.handleSetupPgVector); err != nil {
		return err
	}
	return nil
}
