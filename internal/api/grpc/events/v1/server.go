package v1

import (
	context "context"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	queryevents "github.com/everstacklabs/everstack/internal/query/handlers/events"
	eventspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/events/v1"
	eventsconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/events/v1/eventsconnect"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type Server struct {
	ctx                 context.Context
	serviceInterceptors []connect.Interceptor
}

type GrpcServer struct {
	eventspb.UnimplementedEventsServiceServer
}

func CreateServerWithContext(ctx context.Context) *Server { return &Server{ctx: ctx} }

// WithInterceptors adds service-specific interceptors that run before the
// global interceptor chain (e.g. feature gate).
func (s *Server) WithInterceptors(interceptors ...connect.Interceptor) *Server {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	all := make([]connect.Interceptor, 0, len(s.serviceInterceptors)+len(interceptors))
	all = append(all, s.serviceInterceptors...)
	all = append(all, interceptors...)
	return eventsconnect.NewEventsServiceHandler(s, connect.WithInterceptors(all...))
}
func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return eventspb.File_everstack_events_v1_events_service_proto
}
func (s *Server) AppName() string      { return eventsconnect.EventsServiceName }
func (s *Server) MethodPrefix() string { return eventsconnect.EventsServiceName }

// Methods
func (s *Server) ListEvents(ctx context.Context, req *connect.Request[eventspb.ListEventsRequest], stream *connect.ServerStream[eventspb.ListEventsResponse]) error {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	// Tier-based retention clamping: Pro tier is limited to last 24 hours.
	monitor := enterprise.LicenseMonitorFromContext(s.ctx)
	if ls := monitor.GetLicenseState(); ls != nil && strings.EqualFold(ls.Tier, "pro") {
		floor := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
		if req.Msg.GetFrom() == "" || req.Msg.GetFrom() < floor {
			req.Msg.From = floor
		}
	}

	// Initial backfill: send one batch according to the request, then tail.
	// This provides historical events immediately, then live updates.
	lastFrom := req.Msg.GetFrom()
	sent := make(map[string]struct{}, 1024)
	if initResp, ierr := queryevents.ListEvents(ctx, sys, req.Msg); ierr == nil {
		if initResp != nil && len(initResp.Events) > 0 {
			// stream the initial batch; track max created_at and sent IDs
			maxCreated := lastFrom
			for _, e := range initResp.Events {
				if e.GetId() == "" {
					continue
				}
				if err := stream.Send(&eventspb.ListEventsResponse{Events: []*eventspb.Event{e}}); err != nil {
					return connect.NewError(connect.CodeInternal, err)
				}
				sent[e.GetId()] = struct{}{}
				if ts := e.GetCreatedAt(); ts != "" {
					if maxCreated == "" || ts > maxCreated {
						maxCreated = ts
					}
				}
			}
			if maxCreated != "" {
				lastFrom = maxCreated
			}
		}
	}
	if lastFrom == "" {
		// default: look back a short window to avoid missing just-inserted rows
		lastFrom = time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339)
	}

	// Tail loop: query periodically and stream any new events beyond lastFrom
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// query with moving window [lastFrom, now]
			cur := &eventspb.ListEventsRequest{
				Type:       req.Msg.GetType(),
				ApiKeyHash: req.Msg.GetApiKeyHash(),
				From:       lastFrom,
				To:         "",
				PageSize:   req.Msg.GetPageSize(),
				PageToken:  "",
			}
			batch, qerr := queryevents.ListEvents(ctx, sys, cur)
			if qerr != nil {
				return connect.NewError(connect.CodeInternal, qerr)
			}
			if batch != nil && len(batch.Events) > 0 {
				for _, e := range batch.Events {
					if e.GetId() == "" {
						continue
					}
					if _, ok := sent[e.GetId()]; ok {
						continue // de-dup already sent
					}
					if err := stream.Send(&eventspb.ListEventsResponse{Events: []*eventspb.Event{e}}); err != nil {
						return connect.NewError(connect.CodeInternal, err)
					}
					sent[e.GetId()] = struct{}{}
					if e.GetCreatedAt() != "" {
						lastFrom = e.GetCreatedAt()
					}
				}
			}
		}
	}
}

func (s *Server) GetEvent(ctx context.Context, req *connect.Request[eventspb.GetEventRequest]) (*connect.Response[eventspb.GetEventResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp, err := queryevents.GetEvent(ctx, sys, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) GetEventPayload(ctx context.Context, req *connect.Request[eventspb.GetEventPayloadRequest]) (*connect.Response[eventspb.GetEventPayloadResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp, err := queryevents.GetEventPayload(ctx, sys, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(resp), nil
}
