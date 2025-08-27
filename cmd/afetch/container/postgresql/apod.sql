-- Create the database (run while connected to e.g. the "postgres" db)
-- CREATE DATABASE nasa;

-- Now connect to it (in psql):
-- \c nasa

-- Create the table in the default "public" schema
CREATE TABLE IF NOT EXISTS public.apod (
    apod_date        DATE PRIMARY KEY,
    title            VARCHAR(255) NOT NULL,
    explanation      TEXT NOT NULL,
    media_type       VARCHAR(10) NOT NULL,
    url              VARCHAR(1024) NOT NULL,
    hdurl            VARCHAR(1024),
    service_version  VARCHAR(10),
    copyright        VARCHAR(255),
    thumbnail_url    VARCHAR(1024)
);
