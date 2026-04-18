// Command secrets-rotate re-encrypts ai_providers.api_key_ciphertext rows
// from an old ND_SECRET_KEY master key to a new one.
//
// Usage:
//
//	secrets-rotate \
//	  --old-key <hex-or-base64> \
//	  --new-key <hex-or-base64> \
//	  --dsn <mysql-dsn>           # or ND_DB_DSN env
//	  [--dry-run] \
//	  [--batch-size 100]
//
// The CLI decrypts each row with the old key and re-encrypts it with the
// new key inside a per-batch transaction, so a failure mid-rotation never
// leaves a mix of half-rotated and corrupt rows in a single batch.
//
// Security: plaintext API keys are held only for the duration of a single
// re-encrypt and zeroed immediately afterwards. Nothing secret is ever
// logged; progress lines reference only the already-public api_key_prefix.
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

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/crypto"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "secrets-rotate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		oldKeyRaw string
		newKeyRaw string
		dsn       string
		dryRun    bool
		batchSize int
	)
	flag.StringVar(&oldKeyRaw, "old-key", "", "old ND_SECRET_KEY (hex or base64, 32 bytes)")
	flag.StringVar(&newKeyRaw, "new-key", "", "new ND_SECRET_KEY (hex or base64, 32 bytes)")
	flag.StringVar(&dsn, "dsn", os.Getenv("ND_DB_DSN"), "MySQL DSN (defaults to ND_DB_DSN env)")
	flag.BoolVar(&dryRun, "dry-run", false, "report how many rows would be rotated without writing")
	flag.IntVar(&batchSize, "batch-size", 100, "rows per transaction")
	flag.Parse()

	if oldKeyRaw == "" || newKeyRaw == "" {
		return errors.New("--old-key and --new-key are required")
	}
	if dsn == "" {
		return errors.New("--dsn or ND_DB_DSN is required")
	}
	if batchSize <= 0 {
		return errors.New("--batch-size must be > 0")
	}

	oldKey, err := decodeKey(oldKeyRaw)
	if err != nil {
		return fmt.Errorf("old-key: %w", err)
	}
	newKey, err := decodeKey(newKeyRaw)
	if err != nil {
		return fmt.Errorf("new-key: %w", err)
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

	var total int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM ai_providers WHERE api_key_ciphertext IS NOT NULL",
	).Scan(&total); err != nil {
		return fmt.Errorf("count rows: %w", err)
	}
	fmt.Printf("secrets-rotate: %d candidate rows\n", total)
	if dryRun {
		fmt.Println("secrets-rotate: dry-run, no writes performed")
		return nil
	}
	if total == 0 {
		return nil
	}

	rotated := 0
	lastID := uint64(0)
	for {
		n, nextID, err := rotateBatch(ctx, db, oldCipher, newCipher, lastID, batchSize)
		if err != nil {
			return fmt.Errorf("batch starting after id=%d: %w", lastID, err)
		}
		if n == 0 {
			break
		}
		rotated += n
		lastID = nextID
		fmt.Printf("secrets-rotate: rotated %d/%d\n", rotated, total)
	}
	fmt.Printf("secrets-rotate: done, %d rows rotated\n", rotated)
	return nil
}

// rotateBatch reads up to batchSize rows with id > afterID, re-encrypts them
// inside a single transaction, and returns (count, maxID) of rows processed.
func rotateBatch(
	ctx context.Context,
	db *sql.DB,
	oldCipher, newCipher *crypto.Cipher,
	afterID uint64,
	batchSize int,
) (int, uint64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx,
		"SELECT id, api_key_ciphertext, api_key_prefix "+
			"FROM ai_providers "+
			"WHERE api_key_ciphertext IS NOT NULL AND id > ? "+
			"ORDER BY id LIMIT ?",
		afterID, batchSize,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("select: %w", err)
	}

	type pending struct {
		id         uint64
		prefix     string
		ciphertext []byte
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.ciphertext, &p.prefix); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("scan: %w", err)
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, fmt.Errorf("rows: %w", err)
	}
	rows.Close()

	if len(batch) == 0 {
		return 0, afterID, nil
	}

	stmt, err := tx.PrepareContext(ctx,
		"UPDATE ai_providers SET api_key_ciphertext = ? WHERE id = ?",
	)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare update: %w", err)
	}
	defer stmt.Close()

	var maxID uint64
	for _, p := range batch {
		plaintext, err := oldCipher.Decrypt(p.ciphertext)
		if err != nil {
			return 0, 0, fmt.Errorf("decrypt id=%d prefix=%s: %w", p.id, p.prefix, err)
		}
		sealed, err := newCipher.Encrypt(plaintext)
		zero(plaintext)
		if err != nil {
			return 0, 0, fmt.Errorf("encrypt id=%d prefix=%s: %w", p.id, p.prefix, err)
		}
		if _, err := stmt.ExecContext(ctx, sealed, p.id); err != nil {
			return 0, 0, fmt.Errorf("update id=%d prefix=%s: %w", p.id, p.prefix, err)
		}
		if p.id > maxID {
			maxID = p.id
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit: %w", err)
	}
	return len(batch), maxID, nil
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

// zero overwrites b with zeros to shorten the lifetime of plaintext secrets
// in process memory. It is best-effort; the Go runtime may still retain
// copies in GC'd frames.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
