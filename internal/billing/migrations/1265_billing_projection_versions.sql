-- +goose Up
-- release migration range 1260-1279 (release-perf, AC-017). A version per report
-- projection (migrations 1261 and 1262) that every statement changing the table bumps,
-- in the same transaction. The core keeps the projection's rows in memory with the
-- version it read them at and reads them again only when the version has moved, so a
-- report does not ship 100,000 unchanged rows out of Postgres on every request. Access is
-- still decided per matter on every request; only row data is kept.
-- token is a fresh random value on every change, so rows kept in memory are only ever
-- reused for the exact database state they were read from (a restored database cannot
-- present a cached token with other rows).
CREATE TABLE billing_projection_versions (
    name TEXT PRIMARY KEY,
    version BIGINT NOT NULL,
    token TEXT NOT NULL
);
INSERT INTO billing_projection_versions (name, version, token) VALUES
    ('billing_unbilled', 0, gen_random_uuid()::TEXT), ('billing_aging', 0, gen_random_uuid()::TEXT);
-- +goose StatementBegin
CREATE FUNCTION billing_projection_bump() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE billing_projection_versions SET version = version + 1, token = gen_random_uuid()::TEXT WHERE name = TG_TABLE_NAME;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER billing_unbilled_version AFTER INSERT OR UPDATE OR DELETE ON billing_unbilled
    FOR EACH STATEMENT EXECUTE FUNCTION billing_projection_bump();
CREATE TRIGGER billing_unbilled_version_truncate AFTER TRUNCATE ON billing_unbilled
    FOR EACH STATEMENT EXECUTE FUNCTION billing_projection_bump();
CREATE TRIGGER billing_aging_version AFTER INSERT OR UPDATE OR DELETE ON billing_aging
    FOR EACH STATEMENT EXECUTE FUNCTION billing_projection_bump();
CREATE TRIGGER billing_aging_version_truncate AFTER TRUNCATE ON billing_aging
    FOR EACH STATEMENT EXECUTE FUNCTION billing_projection_bump();

-- +goose Down
DROP TRIGGER billing_aging_version_truncate ON billing_aging;
DROP TRIGGER billing_aging_version ON billing_aging;
DROP TRIGGER billing_unbilled_version_truncate ON billing_unbilled;
DROP TRIGGER billing_unbilled_version ON billing_unbilled;
DROP FUNCTION billing_projection_bump();
DROP TABLE billing_projection_versions;
