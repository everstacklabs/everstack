-- write your UP migration SQL here
CREATE SCHEMA IF NOT EXISTS everstack;

CREATE TABLE IF NOT EXISTS events (
    stream      TEXT    NOT NULL,
    id          TEXT    NOT NULL,
    type        TEXT    NOT NULL,
    payload     JSONB   NOT NULL,
    created_at  BIGINT  NOT NULL,
    PRIMARY KEY (stream, id, created_at)
);