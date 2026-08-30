ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_trace_input_tokens;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_trace_output_tokens;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_llm_messages_tokens;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_llm_choices_tokens;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_oi_input_tokens;
ALTER TABLE otel_traces DROP INDEX IF EXISTS idx_oi_output_tokens;
