// Package operation stores transaction-scoped idempotency receipts shared by
// command endpoints. A receipt is completed in the same transaction as its
// effect and result; without a result, the reservation must roll back.
package operation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

var (
	// ErrInvalidRequest means the command omitted a valid receipt key, principal,
	// target or request hash.
	ErrInvalidRequest = errors.New("operation: invalid receipt request")
	// ErrInvalidReceipt means a persisted receipt does not contain its canonical
	// hash or public identifier.
	ErrInvalidReceipt = errors.New("operation: invalid persisted receipt")
	// ErrIncompleteReceipt means a receipt reservation was committed without its
	// successful result. Callers must fail closed rather than execute through it.
	ErrIncompleteReceipt = errors.New("operation: incomplete receipt")
)

// PrincipalKind names the identity family that owns an operation key.
type PrincipalKind string

const (
	PrincipalUser  PrincipalKind = "user"
	PrincipalAgent PrincipalKind = "agent"
)

// Principal identifies the actor within a workspace.
type Principal struct {
	Kind PrincipalKind
	ID   uint32
}

// Request names one idempotent operation and the canonical hash of its input.
type Request struct {
	WorkspaceID    uint32
	Principal      Principal
	Kind           string
	Key            string
	TargetKind     string
	TargetPublicID types.PublicID
	Hash           [32]byte
}

// Receipt contains the public operation id and completed response, when one
// exists. internalID is deliberately private; it is only used for the
// completion update and never appears on the wire.
type Receipt struct {
	ID          types.PublicID
	Hash        [32]byte
	Result      json.RawMessage
	CompletedAt *time.Time
	internalID  uint32
}

// Lock reserves and locks the operation key in tx. It does not compare the
// request hash or return a replay decision: the caller must first re-authorize
// the current principal and target, then inspect the returned receipt.
func Lock(ctx context.Context, tx *dbretry.Tx, req Request) (Receipt, error) {
	if tx == nil || req.WorkspaceID == 0 || req.Principal.ID == 0 || !validPrincipal(req.Principal.Kind) ||
		req.Kind == "" || len(req.Kind) > 64 || req.TargetKind == "" || len(req.TargetKind) > 64 ||
		req.TargetPublicID == (types.PublicID{}) ||
		!utf8.ValidString(req.Key) || utf8.RuneCountInString(req.Key) < 1 || utf8.RuneCountInString(req.Key) > 128 ||
		req.Hash == ([32]byte{}) {
		return Receipt{}, ErrInvalidRequest
	}

	queries := generated.New(tx.RawTx())
	if err := queries.CreateOrLockOperationReceipt(ctx, generated.CreateOrLockOperationReceiptParams{
		PublicID:       types.New(),
		WorkspaceID:    req.WorkspaceID,
		PrincipalKind:  generated.OperationReceiptsPrincipalKind(req.Principal.Kind),
		PrincipalID:    req.Principal.ID,
		OperationKind:  req.Kind,
		OperationKey:   req.Key,
		RequestHash:    append([]byte(nil), req.Hash[:]...),
		TargetKind:     req.TargetKind,
		TargetPublicID: req.TargetPublicID,
	}); err != nil {
		return Receipt{}, fmt.Errorf("operation: reserve receipt: %w", err)
	}

	row, err := queries.GetOperationReceiptForUpdate(ctx, generated.GetOperationReceiptForUpdateParams{
		WorkspaceID:   req.WorkspaceID,
		PrincipalKind: generated.OperationReceiptsPrincipalKind(req.Principal.Kind),
		PrincipalID:   req.Principal.ID,
		OperationKind: req.Kind,
		OperationKey:  req.Key,
	})
	if err != nil {
		return Receipt{}, fmt.Errorf("operation: lock receipt: %w", err)
	}
	if row.PublicID == (types.PublicID{}) || len(row.RequestHash) != len(req.Hash) {
		return Receipt{}, ErrInvalidReceipt
	}
	receipt := Receipt{ID: row.PublicID, internalID: row.ID}
	copy(receipt.Hash[:], row.RequestHash)
	if row.HasResultJson {
		result, err := receiptResult(row.ResultJson)
		if err != nil || len(result) == 0 {
			return Receipt{}, ErrInvalidReceipt
		}
		receipt.Result = result
	}
	if row.CompletedAt.Valid {
		completedAt := row.CompletedAt.Time
		receipt.CompletedAt = &completedAt
	}
	if (receipt.CompletedAt == nil && row.HasResultJson) || (receipt.CompletedAt != nil && !row.HasResultJson) {
		return Receipt{}, ErrInvalidReceipt
	}
	if receipt.CompletedAt != nil && (len(receipt.Result) == 0 || !json.Valid(receipt.Result)) {
		return Receipt{}, ErrInvalidReceipt
	}
	return receipt, nil
}

func receiptResult(value any) (json.RawMessage, error) {
	switch result := value.(type) {
	case string:
		return json.RawMessage(result), nil
	case []byte:
		return append(json.RawMessage(nil), result...), nil
	default:
		return nil, ErrInvalidReceipt
	}
}

// Complete persists the exact successful response. The caller must invoke it
// only after its domain writes and event appends have succeeded in the same tx.
func Complete(ctx context.Context, tx *dbretry.Tx, receipt Receipt, result json.RawMessage) error {
	if tx == nil || receipt.internalID == 0 || receipt.ID == (types.PublicID{}) ||
		receipt.CompletedAt != nil || !json.Valid(result) {
		return ErrInvalidReceipt
	}
	rows, err := generated.New(tx.RawTx()).CompleteOperationReceipt(ctx, generated.CompleteOperationReceiptParams{
		ID:         receipt.internalID,
		ResultJson: append(json.RawMessage(nil), result...),
	})
	if err != nil {
		return fmt.Errorf("operation: complete receipt: %w", err)
	}
	if rows != 1 {
		return ErrIncompleteReceipt
	}
	return nil
}

func validPrincipal(kind PrincipalKind) bool {
	return kind == PrincipalUser || kind == PrincipalAgent
}
