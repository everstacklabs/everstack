package config

import (
	"context"

	"github.com/everstacklabs/everstack/internal/config"
	"github.com/everstacklabs/everstack/internal/config/validator"
	configpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/config/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements the ConfigService gRPC server
type Server struct {
	configpb.UnimplementedConfigServiceServer
	service *config.Service
}

// NewServer creates a new ConfigService gRPC server
func NewServer(service *config.Service) *Server {
	return &Server{
		service: service,
	}
}

// ValidateYAML validates a YAML configuration string
func (s *Server) ValidateYAML(ctx context.Context, req *configpb.ValidateYAMLRequest) (*configpb.ValidationResult, error) {
	if req.YamlConfig == "" {
		return nil, status.Error(codes.InvalidArgument, "yaml_config is required")
	}

	result, err := s.service.ValidateYAML(req.YamlConfig)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "validation failed: %v", err)
	}

	return s.convertValidationResult(result), nil
}

// ValidateMap validates a configuration map
func (s *Server) ValidateMap(ctx context.Context, req *configpb.ValidateMapRequest) (*configpb.ValidationResult, error) {
	if req.Config == nil {
		return nil, status.Error(codes.InvalidArgument, "config is required")
	}

	// Convert protobuf Struct to map[string]interface{}
	configMap := req.Config.AsMap()

	result, err := s.service.ValidateMap(configMap)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "validation failed: %v", err)
	}

	return s.convertValidationResult(result), nil
}

// GetValidationStatus returns the current validation status
func (s *Server) GetValidationStatus(ctx context.Context, req *configpb.GetValidationStatusRequest) (*configpb.ValidationStatus, error) {
	status := s.service.GetValidationStatus()
	return s.convertValidationStatus(status), nil
}

// GetValidationHistory returns the validation history
func (s *Server) GetValidationHistory(ctx context.Context, req *configpb.GetValidationHistoryRequest) (*configpb.ValidationHistory, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 10 // Default limit
	}

	history := s.service.GetValidationHistory(limit)

	entries := make([]*configpb.ValidationHistoryEntry, len(history))
	for i, entry := range history {
		entries[i] = &configpb.ValidationHistoryEntry{
			Timestamp:    timestamppb.New(entry.Timestamp),
			State:        s.convertValidationState(entry.State),
			ErrorCount:   int32(entry.ErrorCount),
			WarningCount: int32(entry.WarningCount),
			ConfigHash:   entry.ConfigHash,
			Sections:     entry.Sections,
		}
	}

	return &configpb.ValidationHistory{
		Entries: entries,
	}, nil
}

// GetSchema returns a specific schema
func (s *Server) GetSchema(ctx context.Context, req *configpb.GetSchemaRequest) (*configpb.SchemaResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "schema name is required")
	}

	schema, err := s.service.GetSchema(req.Name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "schema not found: %v", err)
	}

	// Convert schema to protobuf Struct
	schemaStruct, err := structpb.NewStruct(schema)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to convert schema: %v", err)
	}

	return &configpb.SchemaResponse{
		Name:   req.Name,
		Schema: schemaStruct,
	}, nil
}

// ListSchemas returns available schemas
func (s *Server) ListSchemas(ctx context.Context, req *configpb.ListSchemasRequest) (*configpb.ListSchemasResponse, error) {
	schemas := s.service.ListAvailableSchemas()
	return &configpb.ListSchemasResponse{
		SchemaNames: schemas,
	}, nil
}

// convertValidationResult converts internal ValidationResult to protobuf
func (s *Server) convertValidationResult(result *validator.ValidationResult) *configpb.ValidationResult {
	pbResult := &configpb.ValidationResult{
		Valid:       result.Valid,
		ValidatedAt: timestamppb.New(result.ValidatedAt),
	}

	// Convert errors
	pbResult.Errors = make([]*configpb.ValidationError, len(result.Errors))
	for i, err := range result.Errors {
		pbResult.Errors[i] = &configpb.ValidationError{
			Field:    err.Field,
			Message:  err.Message,
			Code:     err.Code,
			Severity: s.convertValidationSeverity(err.Severity),
		}
	}

	// Convert warnings
	pbResult.Warnings = make([]*configpb.ValidationWarning, len(result.Warnings))
	for i, warning := range result.Warnings {
		pbResult.Warnings[i] = &configpb.ValidationWarning{
			Field:   warning.Field,
			Message: warning.Message,
			Code:    warning.Code,
		}
	}

	// Convert status if available
	if result.Status != nil {
		pbResult.Status = s.convertValidationStatus(result.Status)
	}

	return pbResult
}

// convertValidationStatus converts internal ValidationStatus to protobuf
func (s *Server) convertValidationStatus(status *validator.ValidationStatus) *configpb.ValidationStatus {
	return &configpb.ValidationStatus{
		State:             s.convertValidationState(status.State),
		Message:           status.Message,
		ErrorCount:        int32(status.ErrorCount),
		WarningCount:      int32(status.WarningCount),
		LastValidated:     timestamppb.New(status.LastValidated),
		ValidatedSections: status.ValidatedSections,
		FailedSections:    status.FailedSections,
	}
}

// convertValidationState converts internal ValidationState to protobuf
func (s *Server) convertValidationState(state validator.ValidationState) configpb.ValidationState {
	switch state {
	case validator.ValidationStateValid:
		return configpb.ValidationState_VALIDATION_STATE_VALID
	case validator.ValidationStateInvalid:
		return configpb.ValidationState_VALIDATION_STATE_INVALID
	case validator.ValidationStateWarning:
		return configpb.ValidationState_VALIDATION_STATE_WARNING
	case validator.ValidationStatePartial:
		return configpb.ValidationState_VALIDATION_STATE_PARTIAL
	case validator.ValidationStatePending:
		return configpb.ValidationState_VALIDATION_STATE_PENDING
	case validator.ValidationStateError:
		return configpb.ValidationState_VALIDATION_STATE_ERROR
	default:
		return configpb.ValidationState_VALIDATION_STATE_UNSPECIFIED
	}
}

// convertValidationSeverity converts internal ValidationSeverity to protobuf
func (s *Server) convertValidationSeverity(severity validator.ValidationSeverity) configpb.ValidationSeverity {
	switch severity {
	case validator.ValidationSeverityInfo:
		return configpb.ValidationSeverity_VALIDATION_SEVERITY_INFO
	case validator.ValidationSeverityWarning:
		return configpb.ValidationSeverity_VALIDATION_SEVERITY_WARNING
	case validator.ValidationSeverityError:
		return configpb.ValidationSeverity_VALIDATION_SEVERITY_ERROR
	case validator.ValidationSeverityCritical:
		return configpb.ValidationSeverity_VALIDATION_SEVERITY_CRITICAL
	default:
		return configpb.ValidationSeverity_VALIDATION_SEVERITY_UNSPECIFIED
	}
}
