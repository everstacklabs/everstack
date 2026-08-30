-- Token-bloom-filter skip indexes on the trace IO payloads. Lets a LIKE
-- '%substring%' filter prune granules instead of full-scanning every span.
-- Token size 4 + 4 hash functions is the default tokenbf_v1 shape; bloom
-- false-positive rate ~1% per granule, plenty for human queries.
--
-- The base table already has idx_span_attr_value (a coarse bloom on every
-- value), but it's not tokenised — it matches the full attribute value, not
-- substrings. These four targeted indexes give substring search on the
-- columns customers actually want to grep.
--
-- Granularity 4 keeps the index small while still pruning at ~32k-row
-- granule level. Bumps the parts size by ~2-3% in practice.

ALTER TABLE otel_traces
    ADD INDEX IF NOT EXISTS idx_trace_input_tokens
        SpanAttributes['trace.input'] TYPE tokenbf_v1(32768, 4, 0) GRANULARITY 4;

ALTER TABLE otel_traces
    ADD INDEX IF NOT EXISTS idx_trace_output_tokens
        SpanAttributes['trace.output'] TYPE tokenbf_v1(32768, 4, 0) GRANULARITY 4;

ALTER TABLE otel_traces
    ADD INDEX IF NOT EXISTS idx_llm_messages_tokens
        SpanAttributes['llm.request.messages'] TYPE tokenbf_v1(32768, 4, 0) GRANULARITY 4;

ALTER TABLE otel_traces
    ADD INDEX IF NOT EXISTS idx_llm_choices_tokens
        SpanAttributes['llm.response.choices'] TYPE tokenbf_v1(32768, 4, 0) GRANULARITY 4;

-- OpenInference inputs / outputs use different attribute names. Index those
-- too so multi-semconv full-text works without a re-emit.
ALTER TABLE otel_traces
    ADD INDEX IF NOT EXISTS idx_oi_input_tokens
        SpanAttributes['input.value'] TYPE tokenbf_v1(32768, 4, 0) GRANULARITY 4;

ALTER TABLE otel_traces
    ADD INDEX IF NOT EXISTS idx_oi_output_tokens
        SpanAttributes['output.value'] TYPE tokenbf_v1(32768, 4, 0) GRANULARITY 4;
