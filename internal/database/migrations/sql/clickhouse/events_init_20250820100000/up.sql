CREATE DATABASE IF NOT EXISTS everstack;

CREATE TABLE IF NOT EXISTS events
(
    stream String,
    id String,
    type String,
    payload String,
    created_at UInt64
)
ENGINE = MergeTree
ORDER BY (stream, id, created_at);


