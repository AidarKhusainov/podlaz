package tundiag

const (
	ClassNetworkApplyFailure  Classification = "network_apply_failure"
	ClassNetworkVerifyFailure Classification = "network_verify_failure"
)

func init() {
	primaryClassificationPriority = append([]Classification{
		ClassNetworkApplyFailure,
		ClassNetworkVerifyFailure,
	}, primaryClassificationPriority...)
}
