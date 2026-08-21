CREATE TABLE apod (
    id              INTEGER GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    apod_date            DATE NOT NULL UNIQUE,
    explanation     TEXT NOT NULL,
    hdurl           TEXT,
    media_type      VARCHAR(20) NOT NULL,
    service_version VARCHAR(10) NOT NULL,
    title           TEXT NOT NULL,
    url             TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
