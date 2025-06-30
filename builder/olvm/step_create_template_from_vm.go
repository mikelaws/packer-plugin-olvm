package olvm

import (
	"context"
	"fmt"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

type stepCreateTemplateFromVM struct {
	Debug bool
}

func (s *stepCreateTemplateFromVM) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	config := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	conn := state.Get("conn").(*ovirtsdk4.Connection)

	vmID := state.Get("vm_id").(string)
	if vmID == "" {
		ui.Error("No VM ID found in state")
		return multistep.ActionHalt
	}

	// Check if the VM is stopped before creating template
	ui.Say(fmt.Sprintf("Checking VM status before creating template..."))
	vmResp, err := conn.SystemService().
		VmsService().
		VmService(vmID).
		Get().
		Send()
	if err != nil {
		err = fmt.Errorf("Error getting VM status: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	vmStatus := string(vmResp.MustVm().MustStatus())
	ui.Message(fmt.Sprintf("Current VM status: %s", vmStatus))

	// Ensure VM is stopped before creating template
	if vmStatus != string(ovirtsdk4.VMSTATUS_DOWN) {
		err = fmt.Errorf("VM must be stopped before creating template. Current status: %s", vmStatus)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	ui.Say(fmt.Sprintf("Creating template '%s' from VM...", config.DestinationTemplateName))

	// Get the Templates service
	templatesService := conn.SystemService().TemplatesService()

	// Create template from VM
	templateBuilder := ovirtsdk4.NewTemplateBuilder().
		Name(config.DestinationTemplateName).
		Description(config.DestinationTemplateDescription)

	// Set the cluster
	clusterBuilder := ovirtsdk4.NewClusterBuilder().
		Name(config.Cluster)
	cluster, err := clusterBuilder.Build()
	if err != nil {
		err = fmt.Errorf("Error creating cluster object: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}
	templateBuilder.Cluster(cluster)

	// Set the source VM
	vmBuilder := ovirtsdk4.NewVmBuilder().Id(vmID)
	templateBuilder.VmBuilder(vmBuilder)

	template, err := templateBuilder.Build()
	if err != nil {
		err = fmt.Errorf("Error creating template object: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	// Create the template from the VM
	templateResp, err := templatesService.Add().
		Template(template).
		Send()
	if err != nil {
		err = fmt.Errorf("Error creating template from VM: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	// Get the created template
	createdTemplate, ok := templateResp.Template()
	if !ok {
		err = fmt.Errorf("No template returned from creation")
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	templateID := createdTemplate.MustId()
	ui.Say(fmt.Sprintf("Template created with ID: %s", templateID))

	// Wait for template to reach OK state
	ui.Message(fmt.Sprintf("Waiting for template '%s' to reach OK state...", config.DestinationTemplateName))
	templateStateChange := StateChangeConf{
		Pending:   []string{"locked", "image_locked"},
		Target:    []string{"ok"},
		Refresh:   TemplateStateRefreshFunc(conn, templateID),
		StepState: state,
	}
	if _, err := WaitForState(&templateStateChange); err != nil {
		err := fmt.Errorf("Error waiting for template (%s) to reach OK state: %s", templateID, err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	ui.Say(fmt.Sprintf("Successfully created template '%s' (ID: %s)", config.DestinationTemplateName, templateID))

	// Store the template name and ID in state for potential use by other steps
	state.Put("template_name", config.DestinationTemplateName)
	state.Put("template_id", templateID)

	return multistep.ActionContinue
}

func (s *stepCreateTemplateFromVM) Cleanup(state multistep.StateBag) {
	// Nothing to cleanup for this step
}
