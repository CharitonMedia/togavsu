package trust

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/CharitonMedia/togavsu/internal/domain"
)

// idem runs fn once per (actor, operation, Idempotency-Key) inside the caller's
// transaction. A replay with the same body returns the stored result and writes and emits
// nothing. The same key with a different body is a conflict. An empty key always runs fn.
func idem[T any](ctx context.Context, s *Service, who domain.Principal, op, key string, body any, fn func() (T, error)) (T, bool, error) {
	var zero T
	if key == "" {
		v, err := fn()
		return v, false, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return zero, false, err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	var stored, response string
	err = s.q(ctx).QueryRowContext(ctx, `SELECT request_hash, response FROM trust_idempotency WHERE actor_id = $1 AND operation = $2 AND idem_key = $3`,
		string(actorID(who)), op, key).Scan(&stored, &response)
	switch {
	case err == nil:
		if stored != hash {
			return zero, false, domain.Conflict("idempotency key was already used with a different body")
		}
		var v T
		if err := json.Unmarshal([]byte(response), &v); err != nil {
			return zero, false, err
		}
		return v, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return zero, false, err
	}
	v, err := fn()
	if err != nil {
		return zero, false, err
	}
	out, err := json.Marshal(v)
	if err != nil {
		return zero, false, err
	}
	_, err = s.q(ctx).ExecContext(ctx, `INSERT INTO trust_idempotency (actor_id, operation, idem_key, request_hash, response, created_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		string(actorID(who)), op, key, hash, string(out), micro(s.now()))
	return v, false, err
}
