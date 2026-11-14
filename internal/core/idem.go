package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/CharitonMedia/togavsu/internal/audit"
	"github.com/CharitonMedia/togavsu/internal/domain"
)

// idemResult is one write's answer: its status and JSON body.
type idemResult struct {
	Status int
	Body   any
}

// idempotentTx runs fn in one transaction on the core pool, once per caller, operation
// and Idempotency-Key (contract idempotencyKey), the way trust's idem does for the
// ledger. The same key and body inside api.idempotency_retention_hours replays the
// stored answer and runs nothing, so it writes and emits nothing; the same key with a
// different body is idempotency_conflict. A transaction-scoped advisory lock on the
// triple makes a concurrent repeat wait for the first to commit and then replay it. An
// empty key runs fn in a transaction with nothing stored. fn's writes use audit.Tx on
// ctx, so they commit with the stored answer or not at all.
func (a *App) idempotentTx(ctx context.Context, p domain.Principal, op, key string, body []byte, fn func(ctx context.Context) (idemResult, error)) (res idemResult, replay bool, err error) {
	if len(key) > domain.IdempotencyKeyMaxChars {
		return idemResult{}, false, domain.Invalid("Idempotency-Key", "an Idempotency-Key is at most 200 characters")
	}
	err = audit.WithTx(ctx, a.Pool, func(ctx context.Context) error {
		if key == "" {
			res, err = fn(ctx)
			return err
		}
		tx := audit.Tx(ctx, a.Pool)
		sum := sha256.Sum256(body)
		hash := hex.EncodeToString(sum[:])
		actor := string(p.UserID) + "|" + p.ModuleID + "|" + string(p.TokenID)
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`,
			"togavsu.core_idempotency/"+actor+"/"+op+"/"+key); err != nil {
			return err
		}
		hours, err := a.intSetting(ctx, "api.idempotency_retention_hours")
		if err != nil {
			return err
		}
		keep, err := time.ParseDuration(strconv.Itoa(hours) + "h")
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		var stored, response string
		var status int
		var at int64
		err = tx.QueryRowContext(ctx, `SELECT request_hash, status, response, created_at FROM core_idempotency WHERE actor = $1 AND operation = $2 AND idem_key = $3`,
			actor, op, key).Scan(&stored, &status, &response, &at)
		switch {
		case err == nil && domain.FromUnixMicro(at).Add(keep).After(now):
			if stored != hash {
				return &domain.APIError{Code: domain.CodeIdempotencyConflict, Message: "this Idempotency-Key was used with a different body"}
			}
			res, replay = idemResult{Status: status, Body: json.RawMessage(response)}, true
			return nil
		case err == nil:
			// Past retention: the key is free again.
			if _, err := tx.ExecContext(ctx, `DELETE FROM core_idempotency WHERE actor = $1 AND operation = $2 AND idem_key = $3`, actor, op, key); err != nil {
				return err
			}
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if res, err = fn(ctx); err != nil {
			return err
		}
		out, err := json.Marshal(res.Body)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO core_idempotency (actor, operation, idem_key, request_hash, status, response, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			actor, op, key, hash, res.Status, string(out), domain.UnixMicroUTC(now))
		return err
	})
	return res, replay, err
}

// intSetting reads an integer setting through the registry.
func (a *App) intSetting(ctx context.Context, key string) (int, error) {
	raw, err := a.Settings.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(raw)
}
