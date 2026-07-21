package executor

// NewDNSExecutorWithRunner returns the canonical Linux TUN/DNS/firewall
// composition with an injectable command runner for deterministic boundary tests.
func NewDNSExecutorWithRunner(runner CommandRunner) DNSAwareTunExecutor {
	return newDNSExecutorWithRunner(runner)
}

func newDNSExecutorWithRunner(runner CommandRunner) DNSAwareTunExecutor {
	if runner == nil {
		runner = OSRunner{}
	}
	return DNSAwareTunExecutor{
		Base:     newTunExecutorWithRunner(runner),
		DNS:      ResolvedDNSExecutor{Runner: runner},
		Firewall: NftablesExecutor{Runner: runner},
	}
}
