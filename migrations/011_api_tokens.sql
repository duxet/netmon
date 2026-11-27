-- +migrate Up
-- Read-only API tokens, for scripts and Prometheus while single sign-on is
-- on. token_hash is the SHA-256 of the token, which is only shown when it is
-- made; prefix is its start, to tell tokens apart. created_by is who made
-- it, as the session showed them.
CREATE SEQUENCE api_tokens_id_seq;
CREATE TABLE api_tokens(
    id INTEGER PRIMARY KEY DEFAULT nextval('api_tokens_id_seq'),
    name VARCHAR NOT NULL,
    token_hash BLOB NOT NULL UNIQUE,
    prefix VARCHAR NOT NULL,
    created_by VARCHAR NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ
);

-- +migrate Down
DROP TABLE api_tokens;
DROP SEQUENCE api_tokens_id_seq;
