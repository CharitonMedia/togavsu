-- +goose Up
-- Editor (PRD 8.20). edit_locks and court_profiles are in 0001. A LawCraft profile's
-- own id names its file in a run's profile folder, so two firm profiles may not share it.
CREATE UNIQUE INDEX court_profiles_lawcraft_id_uidx ON court_profiles ((profile_json::jsonb ->> 'id'));

-- +goose Down
DROP INDEX IF EXISTS court_profiles_lawcraft_id_uidx;
