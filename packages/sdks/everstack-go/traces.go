package everstack

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

type traceObservationIDKey struct{}

// TracesResource provides trace inspection and append-only customization.
type TracesResource struct {
	Overlays           *TraceOverlaysResource
	CustomObservations *TraceCustomObservationsResource
	Annotations        *TraceAnnotationsResource

	t *Transport
}

func newTracesResource(t *Transport) *TracesResource {
	return &TracesResource{
		Overlays:           &TraceOverlaysResource{t: t},
		CustomObservations: &TraceCustomObservationsResource{t: t},
		Annotations:        &TraceAnnotationsResource{t: t},
		t:                  t,
	}
}

func (r *TracesResource) Get(ctx context.Context, traceID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/get", map[string]any{"traceId": traceID}, nil, &resp)
}

func (r *TracesResource) GetTree(ctx context.Context, traceID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/tree", map[string]any{"traceId": traceID}, nil, &resp)
}

func (r *TracesResource) GetRich(ctx context.Context, traceID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/rich/get", map[string]any{"traceId": traceID}, nil, &resp)
}

// TraceSpanOptions configures a custom SDK span.
type TraceSpanOptions struct {
	TraceID             string
	Name                string
	Type                string
	ParentObservationID string
	Source              string
	Level               string
	StatusMessage       string
	Model               string
	Input               any
	Output              any
	InputMimeType       string
	OutputMimeType      string
	InputTokens         *int64
	OutputTokens        *int64
	TotalTokens         *int64
	InputCost           *float64
	OutputCost          *float64
	TotalCost           *float64
	Metadata            map[string]string
	Tags                []string
}

// TraceSpan is the mutable span handle passed to WithSpan callbacks.
type TraceSpan struct {
	ID        string
	StartedAt time.Time
	options   TraceSpanOptions
}

func (s *TraceSpan) SetInput(value any, mimeType ...string) {
	data, detected := encodeTracePayload(value, firstString(mimeType))
	s.options.Input = data
	s.options.InputMimeType = detected
}

func (s *TraceSpan) SetOutput(value any, mimeType ...string) {
	data, detected := encodeTracePayload(value, firstString(mimeType))
	s.options.Output = data
	s.options.OutputMimeType = detected
}

func (s *TraceSpan) SetMetadata(metadata map[string]string) {
	if s.options.Metadata == nil {
		s.options.Metadata = map[string]string{}
	}
	for k, v := range metadata {
		s.options.Metadata[k] = v
	}
}

func (s *TraceSpan) SetTags(tags []string) {
	s.options.Tags = tags
}

// WithSpan runs fn inside a custom SDK span. The span is appended at the end
// and may target a trace id before the raw OTEL trace has arrived.
func (r *TracesResource) WithSpan(ctx context.Context, opts TraceSpanOptions, fn func(context.Context, *TraceSpan) error) error {
	if opts.ParentObservationID == "" {
		if parentID, ok := ctx.Value(traceObservationIDKey{}).(string); ok {
			opts.ParentObservationID = parentID
		}
	}
	if opts.Type == "" {
		opts.Type = "SPAN"
	}
	if opts.Source == "" {
		opts.Source = "SDK"
	}

	span := &TraceSpan{
		ID:        newTraceObservationID(),
		StartedAt: time.Now().UTC(),
		options:   opts,
	}
	childCtx := context.WithValue(ctx, traceObservationIDKey{}, span.ID)
	err := fn(childCtx, span)
	recordErr := r.recordSpan(ctx, span, err)
	if err != nil && recordErr != nil {
		return errors.Join(err, recordErr)
	}
	if err != nil {
		return err
	}
	return recordErr
}

func (r *TracesResource) recordSpan(ctx context.Context, span *TraceSpan, spanErr error) error {
	opts := span.options
	input, inputMime := encodeTracePayload(opts.Input, opts.InputMimeType)
	output, outputMime := encodeTracePayload(opts.Output, opts.OutputMimeType)
	level := opts.Level
	statusMessage := opts.StatusMessage
	if spanErr != nil {
		level = "ERROR"
		statusMessage = spanErr.Error()
	}
	if level == "" {
		level = "DEFAULT"
	}
	_, err := r.CustomObservations.Create(ctx, CustomObservationCreate{
		ID:                  span.ID,
		TraceID:             opts.TraceID,
		ParentObservationID: opts.ParentObservationID,
		Name:                opts.Name,
		Type:                opts.Type,
		Source:              opts.Source,
		StartTime:           span.StartedAt,
		EndTime:             time.Now().UTC(),
		Duration:            time.Since(span.StartedAt).Nanoseconds(),
		Level:               level,
		StatusMessage:       statusMessage,
		Model:               opts.Model,
		InputData:           input,
		OutputData:          output,
		InputMimeType:       inputMime,
		OutputMimeType:      outputMime,
		InputTokens:         opts.InputTokens,
		OutputTokens:        opts.OutputTokens,
		TotalTokens:         opts.TotalTokens,
		InputCost:           opts.InputCost,
		OutputCost:          opts.OutputCost,
		TotalCost:           opts.TotalCost,
		Metadata:            opts.Metadata,
		Tags:                opts.Tags,
	})
	return err
}

type TraceOverlaysResource struct {
	t *Transport
}

type TraceOverlayUpdate struct {
	TraceID        string
	DisplayName    string
	InputOverride  string
	OutputOverride string
	Metadata       map[string]string
	Tags           []string
	HiddenSpanIDs  []string
}

func (r *TraceOverlaysResource) Get(ctx context.Context, traceID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/overlay/get", map[string]any{"traceId": traceID}, nil, &resp)
}

func (r *TraceOverlaysResource) Update(ctx context.Context, update TraceOverlayUpdate) (map[string]any, error) {
	body := map[string]any{"traceId": update.TraceID}
	putString(body, "displayName", update.DisplayName)
	putString(body, "inputOverride", update.InputOverride)
	putString(body, "outputOverride", update.OutputOverride)
	putMap(body, "metadata", update.Metadata)
	putStrings(body, "tags", update.Tags)
	putStrings(body, "hiddenSpanIds", update.HiddenSpanIDs)
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/overlay", body, nil, &resp)
}

type TraceCustomObservationsResource struct {
	t *Transport
}

type CustomObservationCreate struct {
	ID                  string
	TraceID             string
	ParentObservationID string
	Name                string
	Type                string
	Source              string
	StartTime           time.Time
	EndTime             time.Time
	Duration            int64
	Level               string
	StatusMessage       string
	Model               string
	InputData           string
	OutputData          string
	InputMimeType       string
	OutputMimeType      string
	InputTokens         *int64
	OutputTokens        *int64
	TotalTokens         *int64
	InputCost           *float64
	OutputCost          *float64
	TotalCost           *float64
	Metadata            map[string]string
	Tags                []string
}

func (r *TraceCustomObservationsResource) Create(ctx context.Context, obs CustomObservationCreate) (map[string]any, error) {
	body := customObservationBody(obs)
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/observations/custom", body, nil, &resp)
}

func (r *TraceCustomObservationsResource) CreateBatch(ctx context.Context, observations []CustomObservationCreate) (map[string]any, error) {
	items := make([]map[string]any, 0, len(observations))
	for _, obs := range observations {
		items = append(items, customObservationBody(obs))
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/observations/custom/batch", map[string]any{"observations": items}, nil, &resp)
}

func (r *TraceCustomObservationsResource) List(ctx context.Context, traceID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/observations/custom/list", map[string]any{"traceId": traceID}, nil, &resp)
}

type TraceAnnotationsResource struct {
	t *Transport
}

type TraceAnnotationCreate struct {
	TraceID       string
	ObservationID string
	Body          string
	Metadata      map[string]string
}

func (r *TraceAnnotationsResource) Create(ctx context.Context, annotation TraceAnnotationCreate) (map[string]any, error) {
	body := map[string]any{"traceId": annotation.TraceID, "body": annotation.Body}
	putString(body, "observationId", annotation.ObservationID)
	putMap(body, "metadata", annotation.Metadata)
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/annotations", body, nil, &resp)
}

func (r *TraceAnnotationsResource) List(ctx context.Context, traceID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/traces/annotations/list", map[string]any{"traceId": traceID}, nil, &resp)
}

func customObservationBody(obs CustomObservationCreate) map[string]any {
	body := map[string]any{
		"traceId": obs.TraceID,
		"name":    obs.Name,
		"type":    obs.Type,
	}
	putString(body, "id", obs.ID)
	putString(body, "parentObservationId", obs.ParentObservationID)
	putString(body, "source", obs.Source)
	putTime(body, "startTime", obs.StartTime)
	putTime(body, "endTime", obs.EndTime)
	putInt64(body, "duration", obs.Duration)
	putString(body, "level", obs.Level)
	putString(body, "statusMessage", obs.StatusMessage)
	putString(body, "model", obs.Model)
	putString(body, "inputData", obs.InputData)
	putString(body, "outputData", obs.OutputData)
	putString(body, "inputMimeType", obs.InputMimeType)
	putString(body, "outputMimeType", obs.OutputMimeType)
	putInt64Ptr(body, "inputTokens", obs.InputTokens)
	putInt64Ptr(body, "outputTokens", obs.OutputTokens)
	putInt64Ptr(body, "totalTokens", obs.TotalTokens)
	putFloat64Ptr(body, "inputCost", obs.InputCost)
	putFloat64Ptr(body, "outputCost", obs.OutputCost)
	putFloat64Ptr(body, "totalCost", obs.TotalCost)
	putMap(body, "metadata", obs.Metadata)
	putStrings(body, "tags", obs.Tags)
	return body
}

func encodeTracePayload(value any, mimeType string) (string, string) {
	if value == nil {
		return "", ""
	}
	if valueString, ok := value.(string); ok {
		if mimeType == "" {
			mimeType = "text/plain"
		}
		return valueString, mimeType
	}
	data, err := json.Marshal(value)
	if err != nil {
		data = []byte(`"` + err.Error() + `"`)
	}
	if mimeType == "" {
		mimeType = "application/json"
	}
	return string(data), mimeType
}

func newTraceObservationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func putString(body map[string]any, key string, value string) {
	if value != "" {
		body[key] = value
	}
}

func putStrings(body map[string]any, key string, value []string) {
	if len(value) > 0 {
		body[key] = value
	}
}

func putMap(body map[string]any, key string, value map[string]string) {
	if len(value) > 0 {
		body[key] = value
	}
}

func putTime(body map[string]any, key string, value time.Time) {
	if !value.IsZero() {
		body[key] = value.UTC().Format(time.RFC3339Nano)
	}
}

func putInt64(body map[string]any, key string, value int64) {
	if value != 0 {
		body[key] = value
	}
}

func putInt64Ptr(body map[string]any, key string, value *int64) {
	if value != nil {
		body[key] = *value
	}
}

func putFloat64Ptr(body map[string]any, key string, value *float64) {
	if value != nil {
		body[key] = *value
	}
}
