package voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	storagesvc "github.com/everstacklabs/everstack/internal/api/grpc/storage/v1"
	"github.com/everstacklabs/everstack/internal/domain/voice_clone"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	voicepb "github.com/everstacklabs/everstack/pkg/grpc/everstack/voice/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/voice/v1/voiceconnect"
	"github.com/google/uuid"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// requireOrgID extracts the tenant/org id from context. The voice
// handlers used to read it from `req.Msg.OrgId`, which let any caller
// operate against any tenant by setting the field — the same shape of
// leak the 2026-05-06 P0 closed elsewhere. All handlers now go through
// this helper.
func requireOrgID(ctx context.Context) (string, error) {
	if id := contextkeys.GetTenantID(ctx); id != "" {
		return id, nil
	}
	return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
}

// Server implements the VoiceService gRPC server.
type Server struct {
	ctx                 context.Context
	repo                voice_clone.Repository
	registry            *gw.Registry
	storageServer       *storagesvc.Server
	serviceInterceptors []connect.Interceptor
}

// CreateServer creates a new voice Server.
func CreateServer(ctx context.Context, repo voice_clone.Repository) *Server {
	return &Server{ctx: ctx, repo: repo}
}

// WithInterceptors adds service-specific interceptors that run before the
// global interceptor chain (e.g. feature gate).
func (s *Server) WithInterceptors(interceptors ...connect.Interceptor) *Server {
	s.serviceInterceptors = append(s.serviceInterceptors, interceptors...)
	return s
}

// SetRegistry sets the provider registry for voice enrollment via DashScope.
func (s *Server) SetRegistry(reg *gw.Registry) {
	s.registry = reg
}

// SetStorageServer sets the storage server for persisting reference audio.
func (s *Server) SetStorageServer(ss *storagesvc.Server) {
	s.storageServer = ss
}

func (s *Server) RegisterConnectServer(interceptors ...connect.Interceptor) (string, http.Handler) {
	all := make([]connect.Interceptor, 0, len(s.serviceInterceptors)+len(interceptors))
	all = append(all, s.serviceInterceptors...)
	all = append(all, interceptors...)
	return voiceconnect.NewVoiceServiceHandler(s, connect.WithInterceptors(all...))
}

func (s *Server) FileDescriptor() protoreflect.FileDescriptor {
	return voicepb.File_everstack_voice_v1_voice_service_proto
}

func (s *Server) AppName() string      { return voiceconnect.VoiceServiceName }
func (s *Server) MethodPrefix() string { return voiceconnect.VoiceServiceName }

func (s *Server) RegisterGateway(_ context.Context, _ *gwruntime.ServeMux, _ string, _ []grpc.DialOption) error {
	return nil
}

// ─── CRUD ─────────────────────────────────────────────────────────────

func (s *Server) CreateVoiceCloneProfile(ctx context.Context, req *connect.Request[voicepb.CreateVoiceCloneProfileRequest]) (*connect.Response[voicepb.CreateVoiceCloneProfileResponse], error) {
	orgID, err := requireOrgID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg

	if msg.Name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("name is required"))
	}

	providerName := msg.Provider
	if providerName == "" {
		providerName = "qwen"
	}
	model := msg.Model
	if model == "" {
		model = "qwen3-tts-vc-2026-01-22"
	}

	profile := &voice_clone.VoiceCloneProfile{
		ID:            uuid.New().String(),
		OrgID:         orgID,
		Name:          msg.Name,
		Description:   msg.Description,
		ReferenceText: msg.ReferenceText,
		Provider:      providerName,
		Model:         model,
		Metadata:      map[string]interface{}{},
	}

	// Require object storage when reference audio is provided.
	if len(msg.ReferenceAudio) > 0 && (s.storageServer == nil || !s.storageServer.HasStorageConfig(ctx, orgID)) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("object storage not configured — required for voice profiles"))
	}

	// If reference audio is provided, call the provider to enroll the voice
	// and store the returned provider voice ID (e.g. DashScope voice name).
	if len(msg.ReferenceAudio) > 0 && s.registry != nil {
		ap, ok := s.registry.GetAudioProvider(providerName)
		if !ok {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("audio provider %q not available", providerName))
		}

		// Check if the provider supports voice enrollment.
		enroller, ok := ap.(gw.VoiceEnroller)
		if !ok {
			// Try unwrapping middleware layers.
			if p, pOk := s.registry.Get(providerName); pOk {
				if inner, iOk := gw.UnwrapTo[gw.VoiceEnroller](p); iOk {
					enroller = inner
					ok = true
				}
			}
		}
		if !ok {
			return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("provider %q does not support voice enrollment", providerName))
		}

		logger.WithFields("provider", providerName, "name", msg.Name).Info("voice: enrolling voice via provider")

		providerVoiceID, err := enroller.EnrollVoice(ctx, msg.ReferenceAudio, msg.ReferenceText, model, msg.Name)
		if err != nil {
			logger.WithError(err).Error("voice: voice enrollment failed")
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("voice enrollment failed: %w", err))
		}

		profile.ProviderVoiceID = providerVoiceID
		logger.WithFields("provider", providerName, "voice_id", providerVoiceID).Info("voice: enrollment succeeded")

		// Persist reference audio to object storage.
		if s.storageServer != nil {
			objectID, uploadErr := s.storageServer.UploadObject(
				ctx, orgID, "voice_audio", "reference.mp3", "audio/mpeg",
				bytes.NewReader(msg.ReferenceAudio), int64(len(msg.ReferenceAudio)),
				"voice_clone_profile", profile.ID,
			)
			if uploadErr != nil {
				logger.WithError(uploadErr).Warn("voice: failed to persist reference audio to storage")
			} else {
				profile.ReferenceAudioObjectID = objectID
				logger.WithFields("object_id", objectID).Info("voice: reference audio persisted to storage")
			}
		}
	}

	if err := s.repo.Create(ctx, profile); err != nil {
		logger.WithError(err).Error("voice: failed to create voice clone profile")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create voice clone profile: %w", err))
	}

	logger.WithFields("profile", profile.Name, "id", profile.ID).Info("voice: created voice clone profile")
	return connect.NewResponse(&voicepb.CreateVoiceCloneProfileResponse{
		Profile: profileToProto(profile),
	}), nil
}

func (s *Server) ListVoiceCloneProfiles(ctx context.Context, req *connect.Request[voicepb.ListVoiceCloneProfilesRequest]) (*connect.Response[voicepb.ListVoiceCloneProfilesResponse], error) {
	orgID, err := requireOrgID(ctx)
	if err != nil {
		return nil, err
	}
	profiles, err := s.repo.ListByOrg(ctx, orgID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pbProfiles := make([]*voicepb.VoiceCloneProfile, len(profiles))
	for i, p := range profiles {
		pbProfiles[i] = profileToProto(p)
	}

	return connect.NewResponse(&voicepb.ListVoiceCloneProfilesResponse{
		Profiles: pbProfiles,
	}), nil
}

func (s *Server) GetVoiceCloneProfile(ctx context.Context, req *connect.Request[voicepb.GetVoiceCloneProfileRequest]) (*connect.Response[voicepb.GetVoiceCloneProfileResponse], error) {
	orgID, err := requireOrgID(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := s.repo.GetByID(ctx, req.Msg.Id, orgID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if profile == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("voice clone profile not found"))
	}

	return connect.NewResponse(&voicepb.GetVoiceCloneProfileResponse{
		Profile: profileToProto(profile),
	}), nil
}

func (s *Server) UpdateVoiceCloneProfile(ctx context.Context, req *connect.Request[voicepb.UpdateVoiceCloneProfileRequest]) (*connect.Response[voicepb.UpdateVoiceCloneProfileResponse], error) {
	orgID, err := requireOrgID(ctx)
	if err != nil {
		return nil, err
	}
	msg := req.Msg
	if msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id is required"))
	}

	profile, err := s.repo.GetByID(ctx, msg.Id, orgID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if profile == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("voice clone profile not found"))
	}

	if msg.Name != nil {
		profile.Name = *msg.Name
	}
	if msg.Description != nil {
		profile.Description = *msg.Description
	}
	if msg.ReferenceText != nil {
		profile.ReferenceText = *msg.ReferenceText
	}

	if err := s.repo.Update(ctx, profile); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("update voice clone profile: %w", err))
	}

	logger.WithFields("id", profile.ID, "name", profile.Name).Info("voice: updated voice clone profile")
	return connect.NewResponse(&voicepb.UpdateVoiceCloneProfileResponse{
		Profile: profileToProto(profile),
	}), nil
}

func (s *Server) DeleteVoiceCloneProfile(ctx context.Context, req *connect.Request[voicepb.DeleteVoiceCloneProfileRequest]) (*connect.Response[voicepb.DeleteVoiceCloneProfileResponse], error) {
	orgID, err := requireOrgID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.repo.Delete(ctx, req.Msg.Id, orgID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logger.WithFields("id", req.Msg.Id).Info("voice: deleted voice clone profile")
	return connect.NewResponse(&voicepb.DeleteVoiceCloneProfileResponse{}), nil
}

// ─── Helpers ──────────────────────────────────────────────────────────

func profileToProto(p *voice_clone.VoiceCloneProfile) *voicepb.VoiceCloneProfile {
	pb := &voicepb.VoiceCloneProfile{
		Id:                           p.ID,
		OrgId:                        p.OrgID,
		Name:                         p.Name,
		Description:                  p.Description,
		ReferenceAudioObjectId:       p.ReferenceAudioObjectID,
		ReferenceAudioDurationSeconds: p.ReferenceAudioDurationSeconds,
		ReferenceText:                p.ReferenceText,
		Provider:                     p.Provider,
		Model:                        p.Model,
		ProviderVoiceId:              p.ProviderVoiceID,
		CreatedBy:                    p.CreatedBy,
		CreatedAt:                    timestamppb.New(p.CreatedAt),
		UpdatedAt:                    timestamppb.New(p.UpdatedAt),
	}
	return pb
}
