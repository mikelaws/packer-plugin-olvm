package olvm

import (
	"context"
	"fmt"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

type stepStopVM struct{}

func (s *stepStopVM) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	ui := state.Get("ui").(packer.Ui)
	conn := state.Get("conn").(*ovirtsdk4.Connection)
	vmID := state.Get("vm_id").(string)

	// First check if the VM is already stopped
	ui.Say(fmt.Sprintf("Checking VM status: %s...", vmID))
	vmResp, err := conn.SystemService().
		VmsService().
		VmService(vmID).
		Get().
		Send()
	if err != nil {
		err = fmt.Errorf("Error getting VM status: %s", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	vmStatus := string(vmResp.MustVm().MustStatus())
	ui.Message(fmt.Sprintf("Current VM status: %s", vmStatus))

	// If VM is already down, no need to stop it
	if vmStatus == string(ovirtsdk4.VMSTATUS_DOWN) {
		ui.Say(fmt.Sprintf("VM %s is already stopped", vmID))
		return multistep.ActionContinue
	}

	// Only stop the VM if it's not already down
	ui.Say(fmt.Sprintf("Stopping VM: %s...", vmID))
	_, err = conn.SystemService().
		VmsService().
		VmService(vmID).
		Stop().
		Send()
	if err != nil {
		err = fmt.Errorf("Error stopping VM: %s", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	ui.Message(fmt.Sprintf("Waiting for VM to stop: %s...", vmID))
	stateChange := StateChangeConf{
		Pending:   []string{string(ovirtsdk4.VMSTATUS_UP)},
		Target:    []string{string(ovirtsdk4.VMSTATUS_DOWN)},
		Refresh:   VMStateRefreshFunc(conn, vmID),
		StepState: state,
	}
	if _, err := WaitForState(&stateChange); err != nil {
		err := fmt.Errorf("Error waiting for VM (%s) to stop: %s", vmID, err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (s *stepStopVM) Cleanup(state multistep.StateBag) {}
