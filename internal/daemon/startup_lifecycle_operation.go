package daemon

import "context"

func runSerializedStartupLifecycleOperation(
	ctx context.Context,
	lock *lifecycleOperationLock,
	fn func() (bootAutostartStartupResult, error),
) (bootAutostartStartupResult, error) {
	if fn == nil {
		return bootAutostartStartupBlocked, nil
	}
	if lock == nil {
		return fn()
	}
	finishMutation, err := lock.beginExternalMutation()
	if err != nil {
		return bootAutostartStartupBlocked, err
	}
	defer finishMutation()
	if err := lock.acquire(ctx); err != nil {
		return bootAutostartStartupBlocked, err
	}
	defer lock.release()
	return fn()
}
