-- Schema for APOD rows fetched from the APOD Basic API
-- (https://science.nasa.gov/wp-json/wp/v2/apod-basic).
-- explanation, credit, and copyright are stored as plain text (HTML stripped).
CREATE TABLE apod (
    id              INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    apod_date       DATE NOT NULL UNIQUE,
    post_id         BIGINT,
    title           TEXT NOT NULL,
    explanation     TEXT NOT NULL,
    credit          TEXT,
    copyright       TEXT,
    alt             TEXT,
    media_type      VARCHAR(20) NOT NULL,
    url             TEXT,
    hdurl           TEXT,
    basic_html_url  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
