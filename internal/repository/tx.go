package repository

import "context"

// Tx is an open database transaction. The service layer passes it back into
// repository calls to group writes; it never learns what backs it.
type Tx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// TxManager starts transactions. RunInTx handles commit/rollback so callers
// cannot leak a transaction by returning early.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}
