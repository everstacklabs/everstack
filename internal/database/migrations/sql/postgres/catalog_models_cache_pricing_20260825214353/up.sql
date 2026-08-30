-- Per-model cache pricing for the model catalog.
--
-- The catalog has carried cache_read_per_1k / cache_write_per_1k in its YAML
-- for many releases, but the loader dropped both, so cost calculation fell
-- back to a hardcoded multiplier of the input rate. That multiplier is correct
-- for Anthropic and Groq and wrong for everyone else, over-charging cached
-- tokens by 5x on OpenAI and Google and 25x on DeepSeek.
--
-- Scale note: these rates are roughly an order of magnitude smaller than input
-- rates (DeepSeek's cache read is 0.000003625 per 1k), so DECIMAL(12, 8) as
-- used by input_cost_per_1k would round them. These columns use a wider scale.
--
-- NOT NULL DEFAULT 0 rather than nullable: zero already means "this model has
-- no published cache rate" throughout the pricing path, and it keeps the
-- existing float64 struct scanning free of NULL handling.

ALTER TABLE catalog_models
    ADD COLUMN IF NOT EXISTS cache_read_cost_per_1k DECIMAL(18, 12) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_write_cost_per_1k DECIMAL(18, 12) NOT NULL DEFAULT 0;
