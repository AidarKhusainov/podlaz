package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

var errTunNotYetUp = errors.New("tracked Xray TUN is not yet up")

func TestTunExecutorApplyAllowsBoundXrayTunToBecomeReadyDuringAddressApply(t *testing.T) {
	device := &notYetReadyTunDevice{err: errTunNotYetUp}
	address := &recordingReadyTunAddress{}
	exec := TunExecutor{
		TunDevice:   device,
		TunAddress:  address,
		Routes:      applySubphaseRoute{},
		PolicyRules: applySubphasePolicyRule{},
	}
	plan := executorPlanForTest()
	plan.TunAddress = rollbackIdentityAddressPlanForTest()

	steps, err := exec.ApplyWithStepSink(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("apply exact bound Xray TUN while address stage makes link ready: %v", err)
	}
	if device.verifyCalls != 0 {
		t.Fatalf("full device readiness verified before address apply: calls=%d", device.verifyCalls)
	}
	if address.applyCalls != 1 {
		t.Fatalf("address apply calls=%d, want 1", address.applyCalls)
	}
	if len(steps) != 1 || steps[0].Kind != "tun-address" {
		t.Fatalf("applied steps=%#v, want exact tun-address step", steps)
	}
}

func TestTunExecutorFinalVerifyStillRequiresDeviceReady(t *testing.T) {
	device := &notYetReadyTunDevice{err: errTunNotYetUp}
	exec := TunExecutor{
		TunDevice:   device,
		TunAddress:  &recordingReadyTunAddress{},
		Routes:      applySubphaseRoute{},
		PolicyRules: applySubphasePolicyRule{},
	}
	plan := executorPlanForTest()
	plan.TunAddress = rollbackIdentityAddressPlanForTest()

	if err := exec.Verify(context.Background(), plan); !errors.Is(err, errTunNotYetUp) {
		t.Fatalf("final verify err=%v, want device readiness failure", err)
	}
	if device.verifyCalls != 1 {
		t.Fatalf("final device verify calls=%d, want 1", device.verifyCalls)
	}
}

type notYetReadyTunDevice struct {
	err         error
	verifyCalls int
}

func (*notYetReadyTunDevice) Create(context.Context, planner.TunDevicePlan) (Step, error) {
	return Step{}, errors.New("unexpected TUN create")
}

func (e *notYetReadyTunDevice) Verify(context.Context, planner.TunDevicePlan) error {
	e.verifyCalls++
	return e.err
}

func (*notYetReadyTunDevice) Rollback(context.Context, planner.TunDevicePlan) error {
	return nil
}

type recordingReadyTunAddress struct {
	applyCalls int
}

func (e *recordingReadyTunAddress) Bind(_ context.Context, plan planner.TunAddressPlan, _ TunLinkCreationProof) (planner.TunAddressPlan, error) {
	return plan, nil
}

func (e *recordingReadyTunAddress) Apply(_ context.Context, plan planner.TunAddressPlan) (Step, error) {
	e.applyCalls++
	return Step{Kind: "tun-address", Target: plan.Interface + " " + plan.CIDR, Owner: OwnerTunAddress}, nil
}

func (*recordingReadyTunAddress) Verify(context.Context, planner.TunAddressPlan) error {
	return nil
}

func (*recordingReadyTunAddress) Rollback(context.Context, planner.TunAddressPlan) error {
	return nil
}
