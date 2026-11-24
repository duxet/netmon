-- +migrate Up
-- People signed in through single sign-on. token_hash is the SHA-256 of the
-- value of their session cookie, so a copy of the database can't be used to
-- sign in. name and email are what the provider said when they signed in,
-- for showing who it is.
CREATE TABLE sessions(
    token_hash BLOB PRIMARY KEY,
    subject VARCHAR NOT NULL,
    name VARCHAR NOT NULL,
    email VARCHAR NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

-- +migrate Down
DROP TABLE sessions;
