-- Migrates an apod table created for the retired api.nasa.gov/planetary/apod
-- API to the columns written for the APOD Basic API. Existing rows keep
-- their data; the new columns are NULL until the dates are re-fetched.
-- service_version no longer exists in the new API.
ALTER TABLE apod
    DROP COLUMN IF EXISTS service_version,
    ADD COLUMN IF NOT EXISTS post_id        BIGINT,
    ADD COLUMN IF NOT EXISTS credit         TEXT,
    ADD COLUMN IF NOT EXISTS copyright      TEXT,
    ADD COLUMN IF NOT EXISTS alt            TEXT,
    ADD COLUMN IF NOT EXISTS basic_html_url TEXT;
