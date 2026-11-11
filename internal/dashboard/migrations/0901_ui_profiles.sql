-- +goose Up
-- PRD 8.17 profile fields the section 5 users table has no column for: phone, chosen
-- initials and the profile picture (PNG, JPEG or WebP, base64, capped in fixed.json).
-- Portable SQL: the picture is TEXT, not a binary type.
CREATE TABLE user_profiles (
  user_id TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  updated_at BIGINT NOT NULL,
  phone TEXT NOT NULL DEFAULT '',
  initials TEXT NOT NULL DEFAULT '',
  picture_type TEXT NOT NULL DEFAULT '',
  picture TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE user_profiles;
