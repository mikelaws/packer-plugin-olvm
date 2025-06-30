package olvm

import (
	"context"
	"fmt"
	"log"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

type stepCreateVMFromTemplate struct {
	Debug bool
	Ctx   interpolate.Context
}

func (s *stepCreateVMFromTemplate) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	config := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	conn := state.Get("conn").(*ovirtsdk4.Connection)

	ui.Say("Creating virtual machine...")

	cResp, err := conn.SystemService().
		ClustersService().
		List().
		Send()
	if err != nil {
		err := fmt.Errorf("Error getting cluster list: %s", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	var clusterID string
	if clusters, ok := cResp.Clusters(); ok {
		for _, cluster := range clusters.Slice() {
			if clusterName, ok := cluster.Name(); ok {
				if clusterName == config.Cluster {
					clusterID = cluster.MustId()
					log.Printf("Using cluster id: %s", clusterID)
					break
				}
			}
		}
	}
	if clusterID == "" {
		err = fmt.Errorf("Could not find cluster '%s'", config.Cluster)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	var templateID string
	if config.SourceTemplateID != "" {
		templateID = config.SourceTemplateID
	} else {
		templatesService := conn.SystemService().TemplatesService()
		log.Printf("Searching for template '%s'", config.SourceTemplateName)
		tpsResp, err := templatesService.List().
			Search(fmt.Sprintf("name=%s", config.SourceTemplateName)).
			Send()
		if err != nil {
			err = fmt.Errorf("Error searching templates: %s", err)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}
		tpSlice, _ := tpsResp.Templates()

		for _, tp := range tpSlice.Slice() {
			if tp.MustVersion().MustVersionNumber() == int64(config.SourceTemplateVersion) {
				templateID = tp.MustId()
				break
			}
		}
		if templateID == "" {
			err = fmt.Errorf("Could not find template '%s' with version '%d'", config.SourceTemplateName, config.SourceTemplateVersion)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}
	}
	log.Printf("Using template id: %s", templateID)

	// Get template details to use as defaults for CPU and memory
	templateResp, err := conn.SystemService().
		TemplatesService().
		TemplateService(templateID).
		Get().
		Send()
	if err != nil {
		err = fmt.Errorf("Error getting template details: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	template := templateResp.MustTemplate()
	templateCpu := template.MustCpu()
	templateMemory := template.MustMemory()

	// Use config values if specified, otherwise use template defaults
	cpuCount := config.VmVcpuCount
	if cpuCount == 0 {
		if templateCpuTopology, ok := templateCpu.Topology(); ok {
			if templateCores, ok := templateCpuTopology.Cores(); ok {
				cpuCount = int(templateCores)
			}
		}
		if cpuCount == 0 {
			cpuCount = 1 // fallback default
		}
	}

	memoryMB := config.VmMemoryMB
	if memoryMB == 0 {
		memoryMB = int(templateMemory / (1024 * 1024)) // Convert bytes to MB
		if memoryMB == 0 {
			memoryMB = 1024 // fallback default (1GB)
		}
	}

	log.Printf("VM CPU count: %d cores per socket (1 socket, total: %d cores) (template default: %d)", cpuCount, cpuCount, config.VmVcpuCount)
	log.Printf("VM memory: %d MB (template default: %d MB)", memoryMB, config.VmMemoryMB)

	vmBuilder := ovirtsdk4.NewVmBuilder().
		Name(config.VMName).
		Cpu(
			ovirtsdk4.NewCpuBuilder().
				Topology(
					ovirtsdk4.NewCpuTopologyBuilder().
						Sockets(1).
						Cores(int64(cpuCount)).
						MustBuild(),
				).
				MustBuild(),
		).
		Memory(int64(memoryMB) * 1024 * 1024) // Convert MB to bytes

	cluster, err := ovirtsdk4.NewClusterBuilder().
		Id(clusterID).
		Build()
	if err != nil {
		err = fmt.Errorf("Error creating cluster object: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}
	vmBuilder.Cluster(cluster)

	t, err := ovirtsdk4.NewTemplateBuilder().
		Id(templateID).
		Build()
	if err != nil {
		err = fmt.Errorf("Error creating template object: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}
	vmBuilder.Template(t)

	vm, err := vmBuilder.Build()
	if err != nil {
		err = fmt.Errorf("Error creating VM object: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	vmAddResp, err := conn.SystemService().
		VmsService().
		Add().
		Vm(vm).
		Send()
	if err != nil {
		if _, ok := err.(*ovirtsdk4.NotFoundError); ok {
			err = fmt.Errorf("Could not find virtual machine template '%s'", templateID)
		} else {
			err = fmt.Errorf("Error creating virtual machine: %s", err)
		}
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	newVM, ok := vmAddResp.Vm()
	if !ok {
		state.Put("vm_id", "")
		return multistep.ActionHalt
	}

	vmID := newVM.MustId()
	log.Printf("Virtual machine id: %s", vmID)

	// Attach VM to network if specified
	if config.NetworkName != "" {
		ui.Say(fmt.Sprintf("Attaching VM to network: %s", config.NetworkName))

		// Find the network by name in the cluster
		networksService := conn.SystemService().NetworksService()
		var network *ovirtsdk4.Network

		// First try to find the network by name only
		networksResp, err := networksService.List().
			Search(fmt.Sprintf("name=%s", config.NetworkName)).
			Send()
		if err != nil {
			err = fmt.Errorf("Error searching for network '%s': %s", config.NetworkName, err)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}

		networks, ok := networksResp.Networks()
		if !ok || len(networks.Slice()) == 0 {
			// If not found by name only, try searching in the cluster
			log.Printf("Network '%s' not found by name only, searching in cluster '%s'", config.NetworkName, config.Cluster)

			// Get the cluster's networks
			clusterService := conn.SystemService().ClustersService().ClusterService(clusterID)
			clusterNetworksResp, err := clusterService.NetworksService().List().Send()
			if err != nil {
				err = fmt.Errorf("Error getting networks for cluster '%s': %s", config.Cluster, err)
				ui.Error(err.Error())
				state.Put("error", err)
				return multistep.ActionHalt
			}

			clusterNetworks, ok := clusterNetworksResp.Networks()
			if !ok {
				err = fmt.Errorf("No networks found in cluster '%s'", config.Cluster)
				ui.Error(err.Error())
				state.Put("error", err)
				return multistep.ActionHalt
			}

			// Search for the network by name in cluster networks
			for _, clusterNetwork := range clusterNetworks.Slice() {
				if clusterNetworkName, ok := clusterNetwork.Name(); ok {
					log.Printf("Found cluster network: %s", clusterNetworkName)
					if clusterNetworkName == config.NetworkName {
						network = clusterNetwork
						break
					}
				}
			}

			if network == nil {
				// List all available networks for debugging
				log.Printf("Available networks in cluster '%s':", config.Cluster)
				for _, clusterNetwork := range clusterNetworks.Slice() {
					if clusterNetworkName, ok := clusterNetwork.Name(); ok {
						log.Printf("  - %s", clusterNetworkName)
					}
				}

				err = fmt.Errorf("Could not find network '%s' in cluster '%s'", config.NetworkName, config.Cluster)
				ui.Error(err.Error())
				state.Put("error", err)
				return multistep.ActionHalt
			}
		} else {
			// Found network by name, verify it's in the correct cluster
			for _, foundNetwork := range networks.Slice() {
				if foundNetworkName, ok := foundNetwork.Name(); ok {
					log.Printf("Found network: %s", foundNetworkName)
					if foundNetworkName == config.NetworkName {
						network = foundNetwork
						break
					}
				}
			}

			if network == nil {
				err = fmt.Errorf("Network '%s' found but not in expected format", config.NetworkName, config.Cluster)
				ui.Error(err.Error())
				state.Put("error", err)
				return multistep.ActionHalt
			}
		}

		networkID := network.MustId()
		log.Printf("Found network id: %s", networkID)

		// Determine vNIC profile to use
		vnicProfileName := config.VnicProfile
		if vnicProfileName == "" {
			vnicProfileName = config.NetworkName
		}
		log.Printf("Using vNIC profile: %s", vnicProfileName)

		// Find the vNIC profile
		vnicProfilesService := conn.SystemService().VnicProfilesService()
		vnicProfilesResp, err := vnicProfilesService.List().Send()
		if err != nil {
			err = fmt.Errorf("Error getting vNIC profiles: %s", err)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}

		vnicProfiles, ok := vnicProfilesResp.Profiles()
		if !ok {
			err = fmt.Errorf("No vNIC profiles found")
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}

		// Search for the vNIC profile by name and network
		var vnicProfile *ovirtsdk4.VnicProfile
		for _, profile := range vnicProfiles.Slice() {
			if profileName, ok := profile.Name(); ok {
				if profileName == vnicProfileName {
					// Check if this profile is for the correct network
					if profileNetwork, ok := profile.Network(); ok {
						if profileNetwork.MustId() == networkID {
							vnicProfile = profile
							break
						}
					}
				}
			}
		}

		// If not found by name and network, try by name only
		if vnicProfile == nil {
			log.Printf("vNIC profile '%s' not found for network '%s', searching by name only", vnicProfileName, config.NetworkName)

			for _, profile := range vnicProfiles.Slice() {
				if profileName, ok := profile.Name(); ok {
					if profileName == vnicProfileName {
						vnicProfile = profile
						break
					}
				}
			}
		}

		if vnicProfile == nil {
			// List available vNIC profiles for debugging
			log.Printf("Available vNIC profiles:")
			for _, profile := range vnicProfiles.Slice() {
				if profileName, ok := profile.Name(); ok {
					log.Printf("  - %s", profileName)
				}
			}

			err = fmt.Errorf("Could not find vNIC profile '%s'", vnicProfileName)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}

		vnicProfileID := vnicProfile.MustId()
		log.Printf("Found vNIC profile id: %s", vnicProfileID)

		// Create a NIC for the VM and attach it to the network with vNIC profile
		nicBuilder := ovirtsdk4.NewNicBuilder().
			Name("nic1").
			Network(
				ovirtsdk4.NewNetworkBuilder().
					Id(networkID).
					MustBuild(),
			).
			VnicProfile(
				ovirtsdk4.NewVnicProfileBuilder().
					Id(vnicProfileID).
					MustBuild(),
			).
			OnBoot(true).
			Linked(true)

		nic, err := nicBuilder.Build()
		if err != nil {
			err = fmt.Errorf("Error creating NIC: %s", err)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}

		// Add NIC to VM
		nicAddResp, err := conn.SystemService().
			VmsService().
			VmService(vmID).
			NicsService().
			Add().
			Nic(nic).
			Send()
		if err != nil {
			err = fmt.Errorf("Error adding NIC to VM: %s", err)
			ui.Error(err.Error())
			state.Put("error", err)
			return multistep.ActionHalt
		}

		// Verify the NIC was added successfully
		if addedNic, ok := nicAddResp.Nic(); ok {
			log.Printf("Successfully added NIC with ID: %s", addedNic.MustId())

			// Get the updated VM to verify the NIC is attached
			vmGetResp, err := conn.SystemService().
				VmsService().
				VmService(vmID).
				Get().
				Send()
			if err != nil {
				log.Printf("Warning: Could not get updated VM info: %s", err)
			} else {
				if updatedVM, ok := vmGetResp.Vm(); ok {
					if nics, ok := updatedVM.Nics(); ok {
						log.Printf("VM now has %d NIC(s)", len(nics.Slice()))
						for _, nic := range nics.Slice() {
							if nicName, ok := nic.Name(); ok {
								log.Printf("  - NIC: %s", nicName)
								if nicNetwork, ok := nic.Network(); ok {
									if networkName, ok := nicNetwork.Name(); ok {
										log.Printf("    Network: %s", networkName)
									}
								}
							}
						}
					}
				}
			}
		}

		ui.Say(fmt.Sprintf("Successfully attached VM to network: %s", config.NetworkName))
	}

	ui.Message(fmt.Sprintf("Waiting for VM to become ready (status down)..."))
	stateChange := StateChangeConf{
		Pending:   []string{"image_locked"},
		Target:    []string{string(ovirtsdk4.VMSTATUS_DOWN)},
		Refresh:   VMStateRefreshFunc(conn, vmID),
		StepState: state,
	}
	latestVM, err := WaitForState(&stateChange)
	if err != nil {
		err := fmt.Errorf("Failed waiting for VM (%s) to become down: %s", vmID, err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	state.Put("vm_id", latestVM.(*ovirtsdk4.Vm).MustId())

	return multistep.ActionContinue
}

func (s *stepCreateVMFromTemplate) Cleanup(state multistep.StateBag) {
	config := state.Get("config").(*Config)
	if _, ok := state.GetOk("vm_id"); !ok {
		return
	}

	// Skip cleanup if cleanup_vm is set to false
	if !config.CleanupVM {
		ui := state.Get("ui").(packer.Ui)
		ui.Say(fmt.Sprintf("Skipping VM cleanup due to cleanup_vm setting. VM '%s' will remain in the system.", config.VMName))
		return
	}

	ui := state.Get("ui").(packer.Ui)
	conn := state.Get("conn").(*ovirtsdk4.Connection)
	vmID := state.Get("vm_id").(string)

	// First check if the VM is running before attempting to stop it
	ui.Say(fmt.Sprintf("Checking VM status: %s...", config.VMName))
	vmResp, err := conn.SystemService().
		VmsService().
		VmService(vmID).
		Get().
		Send()
	if err != nil {
		ui.Error(fmt.Sprintf("Error getting VM status: %s", err))
		// Continue with deletion attempt anyway
	} else {
		vmStatus := string(vmResp.MustVm().MustStatus())

		// If VM is already down, no need to stop it
		if vmStatus == string(ovirtsdk4.VMSTATUS_DOWN) {
			ui.Say(fmt.Sprintf("VM '%s' is already stopped", config.VMName))
		} else {
			// Only stop the VM if it's not already down
			ui.Say(fmt.Sprintf("Stopping virtual machine: %s...", config.VMName))
			_, err = conn.SystemService().
				VmsService().
				VmService(vmID).
				Stop().
				Send()
			if err != nil {
				ui.Error(fmt.Sprintf("Error stopping VM '%s': %s", config.VMName, err))
				// Continue with deletion attempt anyway
			} else {
				// Wait for VM to stop
				ui.Message(fmt.Sprintf("Waiting for VM to stop: %s...", config.VMName))
				stateChange := StateChangeConf{
					Pending:   []string{string(ovirtsdk4.VMSTATUS_UP)},
					Target:    []string{string(ovirtsdk4.VMSTATUS_DOWN)},
					Refresh:   VMStateRefreshFunc(conn, vmID),
					StepState: state,
				}
				if _, err := WaitForState(&stateChange); err != nil {
					ui.Error(fmt.Sprintf("Error waiting for VM (%s) to stop: %s", config.VMName, err))
					// Continue with deletion attempt anyway
				}
			}
		}
	}

	ui.Say(fmt.Sprintf("Deleting virtual machine: %s...", config.VMName))

	if _, err := conn.SystemService().VmsService().VmService(vmID).Remove().Send(); err != nil {
		ui.Error(fmt.Sprintf("Error deleting VM '%s', may still be around: %s", config.VMName, err))
	}
}
