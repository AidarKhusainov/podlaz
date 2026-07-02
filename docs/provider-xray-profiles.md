# Provider-backed Xray profiles

podlaz has two Xray profile paths.

Manual and share-URI profiles use the normalized profile model. podlaz parses the known fields, validates the supported protocol and transport combinations, and generates the outbound itself.

Provider-backed Xray profiles keep the provider's original Xray JSON object as source data. podlaz still owns the runtime wrapper: local SOCKS and HTTP inbounds, log policy, runtime paths, daemon lifecycle, and cleanup. Provider inbounds are replaced. Provider outbounds, routing, balancers, and protocol-specific outbound settings are preserved and passed to Xray-core.

This prevents podlaz from becoming a partial Xray schema compiler. If a provider profile contains a transport or protocol that Xray-core understands but podlaz does not model yet, subscription import can preserve it as an `xray-json` profile instead of reducing it to an unusable partial normalized profile.

Safety rules:

- provider configs do not control podlaz local listener addresses or ports;
- provider configs do not control runtime file paths;
- provider configs do not control privileged TUN, route, DNS, nftables, or recovery state;
- raw preserved configs are treated as sensitive and must not be printed in full;
- TUN mode remains unsupported for grouped/provider-owned routing until podlaz can safely derive a single server bypass and runtime policy.

Validation failures should distinguish podlaz safety policy, malformed provider JSON, runtime-mode incompatibility, and Xray-core support failures.
