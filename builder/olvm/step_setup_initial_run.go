package olvm

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/packer-plugin-sdk/communicator"
	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

type stepSetupInitialRun struct {
	Debug bool
	Comm  *communicator.Config
}

// Run executes the Packer build step that configures the initial run setup
func (s *stepSetupInitialRun) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	c := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	connWrapper := state.Get("connWrapper").(*ConnectionWrapper)

	ui.Say("Setting up initial run...")

	vmID := state.Get("vm_id").(string)

	var vmService *ovirtsdk4.VmService
	err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		vmService = conn.SystemService().
			VmsService().
			VmService(vmID)
		return nil
	})
	if err != nil {
		err = fmt.Errorf("Error getting VM service: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	// Build initialization configuration using proper oVirt fields
	initializationBuilder := ovirtsdk4.NewInitializationBuilder()

	// Set SSH username
	if s.Comm.SSHUsername != "" {
		log.Printf("Set SSH user name: %s", s.Comm.SSHUsername)
		initializationBuilder.UserName(s.Comm.SSHUsername)
	}

	// Set SSH public key
	if string(s.Comm.SSHPublicKey) != "" {
		publicKey := strings.TrimSpace(string(s.Comm.SSHPublicKey))
		log.Printf("Set authorized SSH key: %s", publicKey)
		initializationBuilder.AuthorizedSshKeys(publicKey)
	}

	// Set VM hostname
	if c.VMName != "" {
		log.Printf("Set VM hostname: %s", c.VMName)
		initializationBuilder.HostName(c.VMName)
	}

	// Configure network if IP address is provided
	if c.IPAddress != "" {
		log.Printf("Configuring static IP: %s/%s", c.IPAddress, c.Netmask)
		log.Printf("Gateway: %s", c.Gateway)

		// Create NIC configuration with in-guest network interface name
		ncBuilder := ovirtsdk4.NewNicConfigurationBuilder().
			Name(c.OSInterfaceName).
			BootProtocol(ovirtsdk4.BootProtocol("static")).
			OnBoot(true)

		// Create IP configuration
		ipBuilder := ovirtsdk4.NewIpBuilder().
			Address(c.IPAddress).
			Netmask(c.Netmask).
			Gateway(c.Gateway)

		nc, err := ncBuilder.IpBuilder(ipBuilder).Build()
		if err != nil {
			err = fmt.Errorf("Error setting NIC configuration: %s", err)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}
		initializationBuilder.NicConfigurationsOfAny(nc)

		// Add DNS servers using the dedicated field
		if len(c.DNSServers) > 0 {
			log.Printf("DNS servers: %v", c.DNSServers)
			// Join DNS servers with space separator as expected by oVirt
			dnsString := strings.Join(c.DNSServers, " ")
			initializationBuilder.DnsServers(dnsString)
		}
	}

	// Build the initialization configuration
	initialization, err := initializationBuilder.Build()
	if err != nil {
		err = fmt.Errorf("Error building initialization: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	// Create VM builder with initialization
	vmBuilder := ovirtsdk4.NewVmBuilder().
		Initialization(initialization)

	vm, err := vmBuilder.Build()
	if err != nil {
		err = fmt.Errorf("Error defining VM initialization: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	// Update the VM with the initialization configuration
	ui.Say("Updating VM with cloud-init configuration...")

	err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		_, err := vmService.Update().
			Vm(vm).
			Send()
		return err
	})

	if err != nil {
		err = fmt.Errorf("Error updating VM with initialization: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	ui.Say("Starting virtual machine...")

	err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		_, err := vmService.Start().
			UseCloudInit(true).
			Send()
		return err
	})

	if err != nil {
		err = fmt.Errorf("Error starting VM: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	ui.Message(fmt.Sprintf("Waiting for VM to become ready (status up)..."))
	stateChange := StateChangeConf{
		Pending:   []string{"wait_for_launch", "powering_up"},
		Target:    []string{string(ovirtsdk4.VMSTATUS_UP)},
		Refresh:   VMStateRefreshFuncWithWrapper(connWrapper, vmID),
		StepState: state,
	}
	_, err = WaitForState(&stateChange)
	if err != nil {
		err := fmt.Errorf("Failed waiting for VM (%s) to become up: %s", vmID, err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	ui.Message("VM successfully started!")

	return multistep.ActionContinue
}

// Cleanup any resources that may have been created during the Run phase.
func (s *stepSetupInitialRun) Cleanup(state multistep.StateBag) {
	// Nothing to cleanup for this step.
}
