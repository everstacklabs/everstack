-- Drop MVs and rollup tables and views
DROP VIEW IF EXISTS api_key_usage_view;
DROP VIEW IF EXISTS model_usage_stats_view;
DROP VIEW IF EXISTS load_balancer_stats_view;
DROP TABLE IF EXISTS mv_api_key_usage_hourly;
DROP TABLE IF EXISTS mv_lb_hourly;
DROP TABLE IF EXISTS mv_model_usage_hourly;
DROP TABLE IF EXISTS chat_sessions;
DROP VIEW IF EXISTS mv_api_key_usage_hourly_mv;
DROP VIEW IF EXISTS mv_lb_hourly_mv;
DROP VIEW IF EXISTS mv_model_usage_hourly_mv;
DROP VIEW IF EXISTS mv_chat_sessions;


