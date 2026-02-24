package olvm

import (
	"context"
	"fmt"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

// stepPrepareVMForTemplate runs after the VM is stopped and after cleanup_interfaces.
// It applies template-related configuration to the VM so the template will inherit it:
// - Reset Cloud-Init / Initial Run on the VM (so template does not inherit it)
// - Set High Availability on the VM (so template inherits HA)
type stepPrepareVMForTemplate struct{}

func (s *stepPrepareVMForTemplate) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	config := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	connWrapper := state.Get("connWrapper").(*ConnectionWrapper)
	vmID := state.Get("vm_id").(string)

	if vmID == "" {
		ui.Error("No VM ID found in state")
		return multistep.ActionHalt
	}

	// 1. Optionally reset Cloud-Init / Initial Run on the VM so the template does not inherit it.
	// API: PUT /vms/{id} with empty <initialization/> clears initialization (oVirt Engine 4.1.8+).
	// See docs/API_REFERENCES_TEMPLATE_PREP.md and Red Hat Bug 1513684.
	if config.TemplateResetCloudInit != nil && *config.TemplateResetCloudInit {
		ui.Say("Resetting Cloud-Init / Initial Run on VM so template will not inherit it...")
		emptyInit, buildErr := ovirtsdk4.NewInitializationBuilder().Build()
		if buildErr != nil {
			err := fmt.Errorf("Error building empty initialization: %s", buildErr)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}
		vmUpdate := ovirtsdk4.NewVmBuilder().Initialization(emptyInit)
		vmForUpdate, buildErr := vmUpdate.Build()
		if buildErr != nil {
			err := fmt.Errorf("Error building VM update for clearing initialization: %s", buildErr)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}
		err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
			_, err := conn.SystemService().
				VmsService().
				VmService(vmID).
				Update().
				Vm(vmForUpdate).
				Send()
			return err
		})
		if err != nil {
			err = fmt.Errorf("Error clearing VM initialization before template creation: %s", err)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}
		ui.Message("VM Cloud-Init / Initial Run settings cleared.")
	} else {
		ui.Message("Keeping VM Cloud-Init / Initial Run settings on template (template_reset_cloud_init is false).")
	}

	// 2. Optionally set High Availability on the VM so the template will inherit it.
	// API: PUT /vms/{id} with high_availability.enabled=true. See docs/API_REFERENCES_TEMPLATE_PREP.md.
	// go-ovirt HighAvailabilityBuilder only supports Enabled and Priority; lease/resume set in UI if needed.
	if config.TemplateHighAvailability {
		ui.Say("Setting High Availability on VM (template will inherit)...")
		if config.TemplateHALeaseStorageDomain != "" {
			ui.Message(fmt.Sprintf("Note: template_ha_lease_storage_domain (%s) is not supported by the SDK; configure VM Lease in the OLVM UI after creation if needed.", config.TemplateHALeaseStorageDomain))
		}
		if config.TemplateHAResumeBehavior != "" {
			ui.Message(fmt.Sprintf("Note: template_ha_resume_behavior (%s) is not supported by the SDK; configure Resume behavior in the OLVM UI after creation if needed.", config.TemplateHAResumeBehavior))
		}
		haBuilder := ovirtsdk4.NewHighAvailabilityBuilder().Enabled(true)
		ha, buildErr := haBuilder.Build()
		if buildErr != nil {
			err := fmt.Errorf("Error building high availability config: %s", buildErr)
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
		vmUpdate := ovirtsdk4.NewVmBuilder().HighAvailability(ha)
		vmForUpdate, buildErr := vmUpdate.Build()
		if buildErr != nil {
			err := fmt.Errorf("Error building VM update for high availability: %s", buildErr)
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
		err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
			_, err := conn.SystemService().
				VmsService().
				VmService(vmID).
				Update().
				Vm(vmForUpdate).
				Send()
			return err
		})
		if err != nil {
			err = fmt.Errorf("Error setting VM high availability: %s", err)
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
		ui.Message("VM High Availability configured (template will inherit).")
	}

	return multistep.ActionContinue
}

func (s *stepPrepareVMForTemplate) Cleanup(state multistep.StateBag) {}
