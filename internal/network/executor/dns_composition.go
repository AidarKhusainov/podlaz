package executor

// NewDNSExecutorWithRunner derives the deterministic test composition from the
// canonical production constructor and replaces only command execution. A nil
// runner returns the production composition unchanged.
func NewDNSExecutorWithRunner(runner CommandRunner) DNSAwareTunExecutor {
	executor := NewOSDNSExecutor()
	if runner == nil {
		return executor
	}

	tunDevice, ok := executor.Base.TunDevice.(IPTunDeviceExecutor)
	if !ok {
		panic("canonical TUN device executor does not support command-runner injection")
	}
	tunDevice.Runner = runner
	executor.Base.TunDevice = tunDevice

	routes, ok := executor.Base.Routes.(IPRouteExecutor)
	if !ok {
		panic("canonical route executor does not support command-runner injection")
	}
	routes.Runner = runner
	executor.Base.Routes = routes

	policyRules, ok := executor.Base.PolicyRules.(IPPolicyRuleExecutor)
	if !ok {
		panic("canonical policy-rule executor does not support command-runner injection")
	}
	policyRules.Runner = runner
	executor.Base.PolicyRules = policyRules

	dns, ok := executor.DNS.(ResolvedDNSExecutor)
	if !ok {
		panic("canonical DNS executor does not support command-runner injection")
	}
	dns.Runner = runner
	executor.DNS = dns

	firewall, ok := executor.Firewall.(NftablesExecutor)
	if !ok {
		panic("canonical firewall executor does not support command-runner injection")
	}
	firewall.Runner = runner
	executor.Firewall = firewall

	return executor
}
