package olvm

import (
	"context"
	"fmt"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

type stepCleanupInterfaces struct{}

func (s *stepCleanupInterfaces) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	config := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	conn := state.Get("conn").(*ovirtsdk4.Connection)
	vmID := state.Get("vm_id").(string)

	// Skip interface cleanup if cleanup_interfaces is set to false
	if !config.CleanupInterfaces {
		ui.Say("Skipping network interface cleanup due to cleanup_interfaces setting")
		return multistep.ActionContinue
	}

	ui.Say("Removing network interfaces from VM...")

	// Get the VM's network interfaces
	vmService := conn.SystemService().VmsService().VmService(vmID)
	nicsResp, err := vmService.NicsService().List().Send()
	if err != nil {
		err = fmt.Errorf("Error getting VM network interfaces: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	nics, ok := nicsResp.Nics()
	if !ok {
		ui.Say("No network interfaces found on VM")
		return multistep.ActionContinue
	}

	// Remove each network interface
	for _, nic := range nics.Slice() {
		nicID := nic.MustId()
		nicName := nic.MustName()

		ui.Message(fmt.Sprintf("Removing network interface: %s (ID: %s)", nicName, nicID))

		_, err := vmService.NicsService().NicService(nicID).Remove().Send()
		if err != nil {
			err = fmt.Errorf("Error removing network interface %s: %s", nicName, err)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}

		ui.Message(fmt.Sprintf("Successfully removed network interface: %s", nicName))
	}

	ui.Say("Successfully removed all network interfaces from VM")
	return multistep.ActionContinue
}

func (s *stepCleanupInterfaces) Cleanup(state multistep.StateBag) {
	// Nothing to cleanup for this step
}
