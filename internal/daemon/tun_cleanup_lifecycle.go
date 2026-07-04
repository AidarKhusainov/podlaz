package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/api"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func (m *XrayManager) runTunCleanup(ctx context.Context, transactionID string) (api.LifecycleResponse, error) {
	store := txstate.TransactionStore{RuntimeDir: m.runtimeDir()}
	tx, _, err := store.Load(transactionID)
	if err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("load TUN transaction %s: %w", transactionID, err)
	}
	plan := tunPlanFromTransaction(tx)
	if err := rollbackTunTransaction(ctx, store, &tx, plan, m.tunPlanExecutor()); err != nil {
		return api.LifecycleResponse{}, err
	}
	if err := removeTransactionFile(store, transactionID); err != nil {
		return api.LifecycleResponse{}, fmt.Errorf("remove rolled-back TUN transaction %s: %w", transactionID, err)
	}
	m.mu.Lock()
	m.state = inactiveXrayState()
	m.mu.Unlock()
	return lifecycleResponse(inactiveXrayState()), nil
}

func verifyCoreStarted(done <-chan struct{}) error {
	if done == nil {
		return errors.New("missing Xray process completion channel")
	}
	select {
	case <-done:
		return errors.New("Xray exited during startup verification")
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}
