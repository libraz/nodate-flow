// Command secrets-rotate re-encrypts every ciphertext column sealed with
// NF_SECRET_KEY from an old master key to a new one.
//
// All stores share a single master key and HKDF label
// (packages/go-shared/crypto), so rotation MUST cover all of them in one
// run — rotating only one table would permanently break decryption of the
// others once the old key is retired. The covered stores are:
//
//   - ai_providers.api_key_ciphertext        (LLM provider API keys)
//   - identities.mfa_secret_ciphertext       (TOTP secrets)
//   - user_integrations.access_token_ciphertext / refresh_token_ciphertext
//     (personal OAuth tokens)
//
// There is deliberately NO per-store selection flag: a partial rotation is
// never a valid end state.
//
// Usage:
//
//	NF_SECRET_KEY_OLD=<hex-or-base64> \
//	NF_SECRET_KEY_NEW=<hex-or-base64> \
//	secrets-rotate \
//	  --dsn <mysql-dsn>           # or NF_DB_DSN env
//	  [--dry-run] \
//	  [--batch-size 100]
//
// NF_SECRET_KEY_OLD falls back to NF_SECRET_KEY when unset, matching the
// documented flow where the currently deployed key is the "old" one.
//
// Keys are read from the environment (or --old-key/--new-key argv flags,
// which are refused unless --allow-insecure-argv is also given, because
// argv is visible to other local users via ps and lands in shell history).
//
// The CLI decrypts each row with the old key and re-encrypts it with the
// new key inside a per-batch transaction. Rows already sealed with the new
// key are skipped, so an interrupted run can simply be re-executed with the
// same keys until it reports success. After rotating every store the CLI
// re-reads all rows and verifies each blob opens under the NEW key; it only
// exits 0 when that verification passes, so a clean exit means it is safe
// to retire the old key.
//
// Security: plaintext secrets never leave the crypto package (Reencrypt /
// CanDecrypt operate on blobs). Nothing secret is ever logged; progress
// lines reference only table names and internal row ids.
package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	_ "github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
)

// envOldKey and envNewKey name the environment variables holding the
// rotation key pair. envOldKey falls back to crypto.EnvVar (NF_SECRET_KEY).
const (
	envOldKey = "NF_SECRET_KEY_OLD"
	envNewKey = "NF_SECRET_KEY_NEW"
)

// store describes one table whose ciphertext columns are sealed with
// NF_SECRET_KEY. selectSQL must return (id, col...) keyset-paginated by id;
// updateSQL must accept the same columns in order followed by the id.
type store struct {
	name      string
	countSQL  string
	selectSQL string
	updateSQL string
	// numCols is the number of ciphertext columns per row (1 or 2).
	// NULL / empty columns are passed through unchanged.
	numCols int
}

// stores is the exhaustive list of NF_SECRET_KEY ciphertext stores. Adding
// a new encrypted column anywhere in sql/ REQUIRES adding it here, or the
// next key rotation will corrupt it.
var stores = []store{
	{
		name: "ai_providers.api_key_ciphertext",
		countSQL: "SELECT COUNT(*) FROM ai_providers " +
			"WHERE LENGTH(api_key_ciphertext) > 0",
		selectSQL: "SELECT id, api_key_ciphertext FROM ai_providers " +
			"WHERE LENGTH(api_key_ciphertext) > 0 AND id > ? ORDER BY id LIMIT ?",
		updateSQL: "UPDATE ai_providers SET api_key_ciphertext = ? WHERE id = ?",
		numCols:   1,
	},
	{
		name: "identities.mfa_secret_ciphertext",
		countSQL: "SELECT COUNT(*) FROM identities " +
			"WHERE mfa_secret_ciphertext IS NOT NULL AND LENGTH(mfa_secret_ciphertext) > 0",
		selectSQL: "SELECT id, mfa_secret_ciphertext FROM identities " +
			"WHERE mfa_secret_ciphertext IS NOT NULL AND LENGTH(mfa_secret_ciphertext) > 0 " +
			"AND id > ? ORDER BY id LIMIT ?",
		updateSQL: "UPDATE identities SET mfa_secret_ciphertext = ? WHERE id = ?",
		numCols:   1,
	},
	{
		name: "user_integrations.{access,refresh}_token_ciphertext",
		countSQL: "SELECT COUNT(*) FROM user_integrations " +
			"WHERE LENGTH(access_token_ciphertext) > 0 " +
			"OR LENGTH(COALESCE(refresh_token_ciphertext, '')) > 0",
		selectSQL: "SELECT id, access_token_ciphertext, refresh_token_ciphertext " +
			"FROM user_integrations " +
			"WHERE (LENGTH(access_token_ciphertext) > 0 " +
			"OR LENGTH(COALESCE(refresh_token_ciphertext, '')) > 0) " +
			"AND id > ? ORDER BY id LIMIT ?",
		updateSQL: "UPDATE user_integrations " +
			"SET access_token_ciphertext = ?, refresh_token_ciphertext = ? WHERE id = ?",
		numCols: 2,
	},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "secrets-rotate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		oldKeyArgv    string
		newKeyArgv    string
		dsn           string
		dryRun        bool
		batchSize     int
		allowInsecure bool
	)
	flag.StringVar(&oldKeyArgv, "old-key", "",
		"old master key (INSECURE: requires --allow-insecure-argv; prefer "+envOldKey+" env)")
	flag.StringVar(&newKeyArgv, "new-key", "",
		"new master key (INSECURE: requires --allow-insecure-argv; prefer "+envNewKey+" env)")
	flag.StringVar(&dsn, "dsn", os.Getenv("NF_DB_DSN"), "MySQL DSN (defaults to NF_DB_DSN env)")
	flag.BoolVar(&dryRun, "dry-run", false,
		"classify every row (needs rotation / already rotated / undecryptable) without writing")
	flag.IntVar(&batchSize, "batch-size", 100, "rows per transaction")
	flag.BoolVar(&allowInsecure, "allow-insecure-argv", false,
		"permit passing keys via --old-key/--new-key argv (visible in ps and shell history)")
	flag.Parse()

	oldKeyRaw, newKeyRaw, err := resolveKeys(oldKeyArgv, newKeyArgv, allowInsecure)
	if err != nil {
		return err
	}
	if dsn == "" {
		return errors.New("--dsn or NF_DB_DSN is required")
	}
	if batchSize <= 0 {
		return errors.New("--batch-size must be > 0")
	}

	oldKey, err := decodeKey(oldKeyRaw)
	if err != nil {
		return fmt.Errorf("old key: %w", err)
	}
	newKey, err := decodeKey(newKeyRaw)
	if err != nil {
		return fmt.Errorf("new key: %w", err)
	}
	oldCipher, err := crypto.New(oldKey)
	zero(oldKey)
	if err != nil {
		return fmt.Errorf("old cipher: %w", err)
	}
	newCipher, err := crypto.New(newKey)
	zero(newKey)
	if err != nil {
		return fmt.Errorf("new cipher: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping db: %w", err)
	}

	if dryRun {
		return dryRunReport(ctx, db, oldCipher, newCipher, batchSize)
	}

	for _, st := range stores {
		var total int
		if err := db.QueryRowContext(ctx, st.countSQL).Scan(&total); err != nil {
			return fmt.Errorf("%s: count rows: %w", st.name, err)
		}
		fmt.Printf("secrets-rotate: %s: %d candidate rows\n", st.name, total)

		rotated := 0
		skipped := 0
		lastID := uint64(0)
		for {
			res, err := rotateBatch(ctx, db, oldCipher, newCipher, st, lastID, batchSize)
			if err != nil {
				return fmt.Errorf(
					"%s: batch starting after id=%d: %w "+
						"(rotation is INCOMPLETE — fix the cause and re-run with the same keys; "+
						"do NOT retire the old key until this command exits 0)",
					st.name, lastID, err)
			}
			if res.processed == 0 {
				break
			}
			rotated += res.rotated
			skipped += res.skipped
			lastID = res.maxID
			fmt.Printf("secrets-rotate: %s: rotated %d, already current %d\n",
				st.name, rotated, skipped)
		}
		fmt.Printf("secrets-rotate: %s: done (%d rotated, %d already current)\n",
			st.name, rotated, skipped)
	}

	if err := verifyAll(ctx, db, newCipher, batchSize); err != nil {
		return fmt.Errorf(
			"post-rotation verification FAILED — do NOT retire the old key: %w", err)
	}
	fmt.Println("secrets-rotate: verification passed, all stores decrypt with the new key")
	return nil
}

// resolveKeys returns the raw old/new key material, preferring environment
// variables over argv. Argv keys are refused unless allowInsecure is set,
// because process arguments leak via ps and shell history.
func resolveKeys(oldArgv, newArgv string, allowInsecure bool) (string, string, error) {
	if (oldArgv != "" || newArgv != "") && !allowInsecure {
		return "", "", errors.New(
			"--old-key/--new-key on the command line are visible to other processes; " +
				"set " + envOldKey + " and " + envNewKey + " environment variables instead, " +
				"or pass --allow-insecure-argv to override")
	}
	oldRaw := os.Getenv(envOldKey)
	if oldRaw == "" {
		oldRaw = os.Getenv(crypto.EnvVar)
	}
	newRaw := os.Getenv(envNewKey)
	if allowInsecure {
		if oldArgv != "" {
			oldRaw = oldArgv
		}
		if newArgv != "" {
			newRaw = newArgv
		}
	}
	if oldRaw == "" || newRaw == "" {
		return "", "", errors.New(
			envOldKey + " (or " + crypto.EnvVar + ") and " + envNewKey + " must be set")
	}
	return oldRaw, newRaw, nil
}

// batchResult reports one rotateBatch pass. skipped counts rows whose every
// column was already sealed with the new key (idempotent re-runs).
type batchResult struct {
	processed int
	rotated   int
	skipped   int
	maxID     uint64
}

// rotateBatch reads up to batchSize rows with id > afterID from one store,
// re-encrypts their ciphertext columns inside a single transaction, and
// returns the ids processed. Columns that already open under the new key
// are left untouched; a column that opens under NEITHER key aborts the run.
func rotateBatch(
	ctx context.Context,
	db *sql.DB,
	oldCipher, newCipher *crypto.Cipher,
	st store,
	afterID uint64,
	batchSize int,
) (batchResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return batchResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	batch, err := loadBatch(ctx, tx, st, afterID, batchSize)
	if err != nil {
		return batchResult{}, err
	}
	if len(batch) == 0 {
		return batchResult{maxID: afterID}, nil
	}

	stmt, err := tx.PrepareContext(ctx, st.updateSQL)
	if err != nil {
		return batchResult{}, fmt.Errorf("prepare update: %w", err)
	}
	defer stmt.Close()

	res := batchResult{}
	for _, row := range batch {
		changed := false
		for i, blob := range row.cols {
			if len(blob) == 0 {
				continue // NULL / empty column: nothing sealed here
			}
			sealed, reErr := crypto.Reencrypt(oldCipher, newCipher, blob)
			if reErr != nil {
				if errors.Is(reErr, crypto.ErrAlreadyRotated) {
					continue
				}
				return batchResult{}, fmt.Errorf(
					"id=%d: ciphertext opens under neither key: %w", row.id, reErr)
			}
			row.cols[i] = sealed
			changed = true
		}
		if changed {
			args := make([]any, 0, st.numCols+1)
			for _, blob := range row.cols {
				if len(blob) == 0 {
					args = append(args, nil)
				} else {
					args = append(args, blob)
				}
			}
			args = append(args, row.id)
			if _, err := stmt.ExecContext(ctx, args...); err != nil {
				return batchResult{}, fmt.Errorf("update id=%d: %w", row.id, err)
			}
			res.rotated++
		} else {
			res.skipped++
		}
		res.processed++
		if row.id > res.maxID {
			res.maxID = row.id
		}
	}
	if err := tx.Commit(); err != nil {
		return batchResult{}, fmt.Errorf("commit: %w", err)
	}
	return res, nil
}

// cipherRow is one fetched row: the internal id plus numCols ciphertext
// blobs (nil for NULL columns).
type cipherRow struct {
	id   uint64
	cols [][]byte
}

// querier abstracts *sql.Tx / *sql.DB for loadBatch.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// loadBatch fetches one keyset page of ciphertext rows for a store.
func loadBatch(
	ctx context.Context,
	q querier,
	st store,
	afterID uint64,
	batchSize int,
) ([]cipherRow, error) {
	rows, err := q.QueryContext(ctx, st.selectSQL, afterID, batchSize)
	if err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	var batch []cipherRow
	for rows.Next() {
		row := cipherRow{cols: make([][]byte, st.numCols)}
		dest := make([]any, 0, st.numCols+1)
		dest = append(dest, &row.id)
		for i := range row.cols {
			dest = append(dest, &row.cols[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		batch = append(batch, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}
	return batch, nil
}

// dryRunReport walks every store read-only and classifies each ciphertext
// column as needing rotation, already rotated, or undecryptable. It fails
// when any blob opens under neither key, since a real run would abort there.
func dryRunReport(
	ctx context.Context,
	db *sql.DB,
	oldCipher, newCipher *crypto.Cipher,
	batchSize int,
) error {
	badTotal := 0
	for _, st := range stores {
		needs, current, bad := 0, 0, 0
		lastID := uint64(0)
		for {
			batch, err := loadBatch(ctx, db, st, lastID, batchSize)
			if err != nil {
				return fmt.Errorf("%s: %w", st.name, err)
			}
			if len(batch) == 0 {
				break
			}
			for _, row := range batch {
				for _, blob := range row.cols {
					switch {
					case len(blob) == 0:
					case oldCipher.CanDecrypt(blob):
						needs++
					case newCipher.CanDecrypt(blob):
						current++
					default:
						bad++
						fmt.Printf("secrets-rotate: dry-run: %s id=%d opens under NEITHER key\n",
							st.name, row.id)
					}
				}
				if row.id > lastID {
					lastID = row.id
				}
			}
		}
		fmt.Printf("secrets-rotate: dry-run: %s: %d to rotate, %d already current, %d undecryptable\n",
			st.name, needs, current, bad)
		badTotal += bad
	}
	if badTotal > 0 {
		return fmt.Errorf("dry-run found %d undecryptable blobs; rotation would abort", badTotal)
	}
	fmt.Println("secrets-rotate: dry-run OK, no writes performed")
	return nil
}

// verifyAll re-reads every ciphertext row in every store and confirms it
// opens under the new key. Rotation only reports success when this passes,
// which is the operator's signal that the old key can be retired.
func verifyAll(
	ctx context.Context,
	db *sql.DB,
	newCipher *crypto.Cipher,
	batchSize int,
) error {
	for _, st := range stores {
		lastID := uint64(0)
		for {
			batch, err := loadBatch(ctx, db, st, lastID, batchSize)
			if err != nil {
				return fmt.Errorf("%s: %w", st.name, err)
			}
			if len(batch) == 0 {
				break
			}
			for _, row := range batch {
				for _, blob := range row.cols {
					if len(blob) == 0 {
						continue
					}
					if !newCipher.CanDecrypt(blob) {
						return fmt.Errorf("%s: id=%d does not decrypt with the new key",
							st.name, row.id)
					}
				}
				if row.id > lastID {
					lastID = row.id
				}
			}
		}
	}
	return nil
}

// decodeKey accepts a 32-byte master key encoded as 64-char hex or base64
// (standard or raw, with or without padding).
func decodeKey(raw string) ([]byte, error) {
	if len(raw) == 64 {
		if b, err := hex.DecodeString(raw); err == nil {
			return b, nil
		}
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("must be 32-byte hex or base64")
}

// zero overwrites b with zeros to shorten the lifetime of key material in
// process memory. It is best-effort; the Go runtime may still retain
// copies in GC'd frames.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
