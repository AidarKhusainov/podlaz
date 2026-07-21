package executor

// NewDNSExecutorWithRunner builds the same Linux TUN/DNS/firewall composition
// used in production while allowing deterministic command fixtures in boundary
// tests. A nil runner selects the production OS runner.
func NewDNSExecutorWithRunner(runner CommandRunner) DNSAwareTunExecutor {
	if runner == nil {
		runner = OSRunner{}
	}
	return DNSAwareTunExecutor{
		Base: TunExecutor{
			TunDevice: IPTunDeviceExecutor{
				Runner:      runner,
				DeviceUser:  defaultTunDeviceUser,
				DeviceGroup: defaultTunDeviceGroup,
			},
			Routes:      IPRouteExecutor{Runner: runner},
			PolicyRules: IPPolicyRuleExecutor{Runner: runner},
		},
		DNS:      ResolvedDNSExecutor{Runner: runner},
		Firewall: NftablesExecutor{Runner: runner},
	}
}
