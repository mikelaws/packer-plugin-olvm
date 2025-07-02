package olvm

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

type stepCreateVM struct {
	Debug bool
	Ctx   interpolate.Context
}

// VMResourceInfo holds information about the source resource (template or disk)
type VMResourceInfo struct {
	ID       string
	Name     string
	CPUCount int
	MemoryMB int
}

func (s *stepCreateVM) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	config := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	conn := state.Get("conn").(*ovirtsdk4.Connection)

	sourceType := config.SourceConfig.GetSourceType()
	ui.Say(fmt.Sprintf("Creating virtual machine from %s...", sourceType))

	// Get cluster ID
	clusterID, err := s.getClusterID(conn, config.Cluster)
	if err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	// Get source resource info (template or disk)
	resourceInfo, err := s.getSourceResourceInfo(conn, config)
	if err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	// Determine CPU and memory values
	cpuCount, memoryMB := s.getVMResources(config, resourceInfo)

	log.Printf("VM CPU count: %d cores per socket (1 socket, total: %d cores)", cpuCount, cpuCount)
	log.Printf("VM memory: %d MB", memoryMB)

	// Create VM
	vmID, err := s.createVM(conn, config, clusterID, cpuCount, memoryMB, resourceInfo)
	if err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	// Attach network if specified
	if config.NetworkName != "" {
		if err := s.manageNetworkInterfaces(conn, config, vmID, clusterID); err != nil {
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
	}

	// Wait for VM to be ready
	if err := s.waitForVMReady(conn, vmID, state); err != nil {
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	// Get the latest VM info
	vmResp, err := conn.SystemService().
		VmsService().
		VmService(vmID).
		Get().
		Send()
	if err != nil {
		err = fmt.Errorf("Error getting VM info: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	latestVM := vmResp.MustVm()
	state.Put("vm_id", latestVM.MustId())

	return multistep.ActionContinue
}

func (s *stepCreateVM) getClusterID(conn *ovirtsdk4.Connection, clusterName string) (string, error) {
	cResp, err := conn.SystemService().
		ClustersService().
		List().
		Send()
	if err != nil {
		return "", fmt.Errorf("Error getting cluster list: %s", err)
	}

	if clusters, ok := cResp.Clusters(); ok {
		for _, cluster := range clusters.Slice() {
			if name, ok := cluster.Name(); ok {
				if name == clusterName {
					clusterID := cluster.MustId()
					log.Printf("Using cluster id: %s", clusterID)
					return clusterID, nil
				}
			}
		}
	}

	return "", fmt.Errorf("Could not find cluster '%s'", clusterName)
}

func (s *stepCreateVM) getSourceResourceInfo(conn *ovirtsdk4.Connection, config *Config) (*VMResourceInfo, error) {
	switch config.SourceConfig.GetSourceType() {
	case "template":
		return s.getTemplateInfo(conn, config)
	case "disk":
		return s.getDiskInfo(conn, config)
	default:
		return nil, fmt.Errorf("Unsupported source type: %s", config.SourceConfig.GetSourceType())
	}
}

func (s *stepCreateVM) getTemplateInfo(conn *ovirtsdk4.Connection, config *Config) (*VMResourceInfo, error) {
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
			return nil, fmt.Errorf("Error searching templates: %s", err)
		}
		tpSlice, _ := tpsResp.Templates()

		for _, tp := range tpSlice.Slice() {
			if tp.MustVersion().MustVersionNumber() == int64(config.SourceTemplateVersion) {
				templateID = tp.MustId()
				break
			}
		}
		if templateID == "" {
			return nil, fmt.Errorf("Could not find template '%s' with version '%d'", config.SourceTemplateName, config.SourceTemplateVersion)
		}
	}
	log.Printf("Using template id: %s", templateID)

	// Get template details
	templateResp, err := conn.SystemService().
		TemplatesService().
		TemplateService(templateID).
		Get().
		Send()
	if err != nil {
		return nil, fmt.Errorf("Error getting template details: %s", err)
	}

	template := templateResp.MustTemplate()
	templateCpu := template.MustCpu()
	templateMemory := template.MustMemory()

	// Extract CPU and memory info
	cpuCount := 1 // default
	if templateCpuTopology, ok := templateCpu.Topology(); ok {
		if templateCores, ok := templateCpuTopology.Cores(); ok {
			cpuCount = int(templateCores)
		}
	}

	memoryMB := int(templateMemory / (1024 * 1024)) // Convert bytes to MB
	if memoryMB == 0 {
		memoryMB = 1024 // fallback default
	}

	return &VMResourceInfo{
		ID:       templateID,
		Name:     config.SourceTemplateName,
		CPUCount: cpuCount,
		MemoryMB: memoryMB,
	}, nil
}

func (s *stepCreateVM) getDiskInfo(conn *ovirtsdk4.Connection, config *Config) (*VMResourceInfo, error) {
	var diskID string
	var diskFound bool
	if config.SourceDiskID != "" {
		diskID = config.SourceDiskID
		// Check if disk with this ID exists
		diskResp, err := conn.SystemService().DisksService().DiskService(diskID).Get().Send()
		if err != nil {
			return nil, fmt.Errorf("Could not find disk with ID '%s': %s", diskID, err)
		}
		if diskResp.MustDisk().MustId() == diskID {
			diskFound = true
		}
	} else {
		disksService := conn.SystemService().DisksService()
		log.Printf("Searching for disk '%s'", config.SourceDiskName)
		disksResp, err := disksService.List().
			Search(fmt.Sprintf("alias=%s", config.SourceDiskName)).
			Send()
		if err != nil {
			return nil, fmt.Errorf("Error searching disks: %s", err)
		}
		diskSlice, _ := disksResp.Disks()

		if len(diskSlice.Slice()) == 0 {
			// Try searching by name if alias search fails
			log.Printf("No disk found with alias '%s', trying name search", config.SourceDiskName)
			disksResp, err = disksService.List().
				Search(fmt.Sprintf("name=%s", config.SourceDiskName)).
				Send()
			if err != nil {
				return nil, fmt.Errorf("Error searching disks by name: %s", err)
			}
			diskSlice, _ = disksResp.Disks()
		}

		if len(diskSlice.Slice()) == 0 {
			return nil, fmt.Errorf("Could not find disk with alias or name '%s'", config.SourceDiskName)
		}

		// Use the first disk found with the alias or name
		diskID = diskSlice.Slice()[0].MustId()
		diskFound = true
	}

	if !diskFound || diskID == "" {
		return nil, fmt.Errorf("No matching disk found for source_disk_id or source_disk_name/alias")
	}

	// Get disk details
	diskResp, err := conn.SystemService().
		DisksService().
		DiskService(diskID).
		Get().
		Send()
	if err != nil {
		return nil, fmt.Errorf("Error getting disk details: %s", err)
	}

	disk := diskResp.MustDisk()
	log.Printf("Found disk: %s (Size: %d bytes)", disk.MustName(), disk.MustProvisionedSize())

	return &VMResourceInfo{
		ID:       diskID,
		Name:     config.SourceDiskName,
		CPUCount: 1,    // default for disk
		MemoryMB: 1024, // default for disk
	}, nil
}

func (s *stepCreateVM) getVMResources(config *Config, resourceInfo *VMResourceInfo) (int, int) {
	// Use config values if specified, otherwise use resource defaults
	cpuCount := config.VmVcpuCount
	if cpuCount == 0 {
		cpuCount = resourceInfo.CPUCount
	}

	memoryMB := config.VmMemoryMB
	if memoryMB == 0 {
		memoryMB = resourceInfo.MemoryMB
	}

	return cpuCount, memoryMB
}

func (s *stepCreateVM) createVM(conn *ovirtsdk4.Connection, config *Config, clusterID string, cpuCount, memoryMB int, resourceInfo *VMResourceInfo) (string, error) {
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
		return "", fmt.Errorf("Error creating cluster object: %s", err)
	}
	vmBuilder.Cluster(cluster)

	// Add template or disk based on source type
	if config.SourceConfig.GetSourceType() == "template" {
		t, err := ovirtsdk4.NewTemplateBuilder().
			Id(resourceInfo.ID).
			Build()
		if err != nil {
			return "", fmt.Errorf("Error creating template object: %s", err)
		}
		vmBuilder.Template(t)

		// Set VirtIO-SCSI enabled based on storage driver for template-based VMs
		virtioScsi, err := ovirtsdk4.NewVirtioScsiBuilder().
			Enabled(config.VMStorageDriver == "virtio-scsi").
			Build()
		if err != nil {
			return "", fmt.Errorf("Error creating VirtIO-SCSI object: %s", err)
		}
		vmBuilder.VirtioScsi(virtioScsi)
	}

	if config.SourceConfig.GetSourceType() == "disk" {
		// For disk-based VMs, we need to use the blank template
		blankTemplate, err := ovirtsdk4.NewTemplateBuilder().
			Name("Blank").
			Build()
		if err != nil {
			return "", fmt.Errorf("Error creating blank template object: %s", err)
		}
		vmBuilder.Template(blankTemplate)

		log.Printf("Creating VM without disk attachment for disk ID: %s", resourceInfo.ID)
		// Note: Disk will be attached after VM creation
	}

	vm, err := vmBuilder.Build()
	if err != nil {
		return "", fmt.Errorf("Error creating VM object: %s", err)
	}

	vmAddResp, err := conn.SystemService().
		VmsService().
		Add().
		Vm(vm).
		Send()
	if err != nil {
		if config.SourceConfig.GetSourceType() == "template" {
			if _, ok := err.(*ovirtsdk4.NotFoundError); ok {
				return "", fmt.Errorf("Could not find virtual machine template '%s'", resourceInfo.ID)
			}
		}
		return "", fmt.Errorf("Error creating virtual machine: %s", err)
	}

	newVM, ok := vmAddResp.Vm()
	if !ok {
		return "", fmt.Errorf("No VM returned from creation")
	}

	vmID := newVM.MustId()
	log.Printf("Virtual machine id: %s", vmID)

	// Attach disk for disk-based VMs after VM creation
	if config.SourceConfig.GetSourceType() == "disk" {
		log.Printf("Cloning disk %s before attaching to VM %s", resourceInfo.ID, vmID)
		clonedDiskID, err := s.cloneDisk(conn, resourceInfo.ID, resourceInfo.Name)
		if err != nil {
			return "", fmt.Errorf("Error cloning disk: %s", err)
		}

		log.Printf("Attaching cloned disk %s to VM %s", clonedDiskID, vmID)
		if err := s.attachDiskToVM(conn, vmID, clonedDiskID, config.VMStorageDriver); err != nil {
			return "", fmt.Errorf("Error attaching cloned disk to VM: %s", err)
		}
	}

	// Verify disk attachment for disk-based VMs
	if config.SourceConfig.GetSourceType() == "disk" {
		log.Printf("Verifying disk attachment for VM %s", vmID)
		vmService := conn.SystemService().VmsService().VmService(vmID)
		vmResp, err := vmService.Get().Send()
		if err != nil {
			log.Printf("Warning: Could not verify disk attachment: %s", err)
		} else {
			vm := vmResp.MustVm()
			if diskAttachments, ok := vm.DiskAttachments(); ok {
				log.Printf("VM has %d disk attachments", len(diskAttachments.Slice()))
				for i, attachment := range diskAttachments.Slice() {
					if disk, ok := attachment.Disk(); ok {
						log.Printf("Disk attachment %d: ID=%s, Interface=%s, Bootable=%t",
							i, disk.MustId(), attachment.MustInterface(), attachment.MustBootable())
					}
				}
			} else {
				log.Printf("Warning: No disk attachments found on VM")
			}
		}
	}

	// If source is disk, do not attach disk post-creation (already attached)

	return vmID, nil
}

func (s *stepCreateVM) cloneDisk(conn *ovirtsdk4.Connection, sourceDiskID, sourceDiskName string) (string, error) {
	// Generate unique name for cloned disk
	epochTimestamp := strconv.FormatInt(time.Now().Unix(), 10)
	clonedDiskName := fmt.Sprintf("%s-%s", sourceDiskName, epochTimestamp)

	log.Printf("Creating disk clone: %s from source disk: %s", clonedDiskName, sourceDiskID)

	// Get source disk information first
	sourceDiskResp, err := conn.SystemService().
		DisksService().
		DiskService(sourceDiskID).
		Get().
		Send()
	if err != nil {
		return "", fmt.Errorf("Error getting source disk information: %s", err)
	}

	sourceDisk := sourceDiskResp.MustDisk()

	// Get storage domain from source disk
	var storageDomainID string
	if storageDomains, ok := sourceDisk.StorageDomains(); ok {
		if len(storageDomains.Slice()) > 0 {
			storageDomainID = storageDomains.Slice()[0].MustId()
		}
	}

	if storageDomainID == "" {
		return "", fmt.Errorf("Could not determine storage domain for source disk")
	}

	log.Printf("Source disk size: %d bytes, storage domain: %s", sourceDisk.MustProvisionedSize(), storageDomainID)

	// Create the cloned disk object
	clonedDisk, err := ovirtsdk4.NewDiskBuilder().
		Name(clonedDiskName).
		Build()
	if err != nil {
		return "", fmt.Errorf("Error creating cloned disk object: %s", err)
	}

	// Create storage domain object for the copy operation
	storageDomain, err := ovirtsdk4.NewStorageDomainBuilder().
		Id(storageDomainID).
		Build()
	if err != nil {
		return "", fmt.Errorf("Error creating storage domain object: %s", err)
	}

	// Copy the disk using the disk service copy API
	log.Printf("Copying disk using disk service copy API")
	_, err = conn.SystemService().
		DisksService().
		DiskService(sourceDiskID).
		Copy().
		Disk(clonedDisk).
		StorageDomain(storageDomain).
		Send()
	if err != nil {
		return "", fmt.Errorf("Error copying disk: %s", err)
	}

	// Poll for the new disk to appear and become OK
	log.Printf("Waiting for cloned disk %s to become available...", clonedDiskName)
	var clonedDiskID string
	for i := 0; i < 30; i++ { // up to ~5 minutes
		disksResp, err := conn.SystemService().DisksService().List().Search(fmt.Sprintf("name=%s", clonedDiskName)).Send()
		if err != nil {
			return "", fmt.Errorf("Error listing disks: %s", err)
		}
		disks := disksResp.MustDisks().Slice()
		if len(disks) > 0 {
			disk := disks[0]
			status, _ := disk.Status()
			if status == ovirtsdk4.DISKSTATUS_OK {
				clonedDiskID = disk.MustId()
				break
			}
		}
		time.Sleep(10 * time.Second)
	}
	if clonedDiskID == "" {
		return "", fmt.Errorf("Timed out waiting for cloned disk to become available")
	}
	log.Printf("Successfully created disk clone: %s (ID: %s)", clonedDiskName, clonedDiskID)
	return clonedDiskID, nil
}

func (s *stepCreateVM) attachDiskToVM(conn *ovirtsdk4.Connection, vmID, diskID, storageDriver string) error {
	// Create disk object
	disk, err := ovirtsdk4.NewDiskBuilder().
		Id(diskID).
		Build()
	if err != nil {
		return fmt.Errorf("Error creating disk object: %s", err)
	}

	// Determine disk interface based on storage driver
	var diskInterface ovirtsdk4.DiskInterface
	if storageDriver == "virtio-scsi" {
		diskInterface = ovirtsdk4.DISKINTERFACE_VIRTIO_SCSI
	} else {
		diskInterface = ovirtsdk4.DISKINTERFACE_VIRTIO
	}

	log.Printf("Attaching disk %s to VM %s with interface %s", diskID, vmID, string(diskInterface))

	// Create disk attachment
	diskAttachment, err := ovirtsdk4.NewDiskAttachmentBuilder().
		Disk(disk).
		Interface(diskInterface).
		Bootable(true).
		Active(true).
		Build()
	if err != nil {
		return fmt.Errorf("Error creating disk attachment: %s", err)
	}

	// Attach disk to VM
	_, err = conn.SystemService().
		VmsService().
		VmService(vmID).
		DiskAttachmentsService().
		Add().
		Attachment(diskAttachment).
		Send()
	if err != nil {
		return fmt.Errorf("Error attaching disk to VM: %s", err)
	}

	log.Printf("Successfully attached disk %s to VM %s", diskID, vmID)
	return nil
}

func (s *stepCreateVM) manageNetworkInterfaces(conn *ovirtsdk4.Connection, config *Config, vmID, clusterID string) error {
	// Find the network
	var network *ovirtsdk4.Network
	networksService := conn.SystemService().NetworksService()
	networksResp, err := networksService.List().Send()
	if err != nil {
		return fmt.Errorf("Error getting networks: %s", err)
	}

	networks, ok := networksResp.Networks()
	if !ok {
		return fmt.Errorf("No networks found")
	}

	for _, net := range networks.Slice() {
		if netName, ok := net.Name(); ok {
			if netName == config.NetworkName {
				network = net
				break
			}
		}
	}

	if network == nil {
		// Try to find network in the cluster
		clusterNetworksService := conn.SystemService().
			ClustersService().
			ClusterService(clusterID).
			NetworksService()
		clusterNetworksResp, err := clusterNetworksService.List().Send()
		if err != nil {
			return fmt.Errorf("Error getting cluster networks: %s", err)
		}

		clusterNetworks, ok := clusterNetworksResp.Networks()
		if ok {
			for _, net := range clusterNetworks.Slice() {
				if netName, ok := net.Name(); ok {
					if netName == config.NetworkName {
						network = net
						break
					}
				}
			}
		}
	}

	if network == nil {
		return fmt.Errorf("Could not find network '%s'", config.NetworkName)
	}

	log.Printf("Found network: %s (ID: %s)", network.MustName(), network.MustId())

	// Find vNIC profile
	var vnicProfile *ovirtsdk4.VnicProfile
	vnicProfileName := config.VnicProfile
	if vnicProfileName == "" {
		vnicProfileName = config.NetworkName
	}

	vnicProfilesService := conn.SystemService().VnicProfilesService()
	vnicProfilesResp, err := vnicProfilesService.List().Send()
	if err != nil {
		return fmt.Errorf("Error getting vNIC profiles: %s", err)
	}

	vnicProfiles, ok := vnicProfilesResp.Profiles()
	if ok {
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
		return fmt.Errorf("Could not find vNIC profile '%s'", vnicProfileName)
	}

	log.Printf("Found vNIC profile: %s (ID: %s)", vnicProfile.MustName(), vnicProfile.MustId())

	// Check for existing network interfaces
	vmService := conn.SystemService().VmsService().VmService(vmID)
	nicsService := vmService.NicsService()
	nicsResp, err := nicsService.List().Send()
	if err != nil {
		return fmt.Errorf("Error getting VM network interfaces: %s", err)
	}

	existingNics, ok := nicsResp.Nics()
	if !ok {
		existingNics = nil
	}

	// For template-based VMs, check if there are existing network interfaces
	if config.SourceConfig.GetSourceType() == "template" && existingNics != nil && len(existingNics.Slice()) > 0 {
		log.Printf("Template has %d existing network interfaces", len(existingNics.Slice()))

		// Use the first existing network interface and configure it
		firstNic := existingNics.Slice()[0]
		nicID := firstNic.MustId()
		nicName := firstNic.MustName()

		log.Printf("Configuring existing network interface: %s (ID: %s)", nicName, nicID)

		// Update the existing NIC with our network configuration
		nicUpdateBuilder := ovirtsdk4.NewNicBuilder().
			Name(nicName).
			Network(
				ovirtsdk4.NewNetworkBuilder().
					Id(network.MustId()).
					MustBuild(),
			).
			VnicProfile(
				ovirtsdk4.NewVnicProfileBuilder().
					Id(vnicProfile.MustId()).
					MustBuild(),
			).
			OnBoot(true).
			Linked(true)

		nicUpdate, err := nicUpdateBuilder.Build()
		if err != nil {
			return fmt.Errorf("Error creating NIC update: %s", err)
		}

		_, err = nicsService.NicService(nicID).Update().Nic(nicUpdate).Send()
		if err != nil {
			return fmt.Errorf("Error updating existing NIC: %s", err)
		}

		log.Printf("Successfully configured existing network interface '%s' with network: %s", nicName, config.NetworkName)
		return nil
	}

	// For disk-based VMs or template-based VMs without existing interfaces, create a new NIC
	log.Printf("Creating new network interface for VM")

	// Create NIC
	nicBuilder := ovirtsdk4.NewNicBuilder().
		Name("nic1").
		Network(
			ovirtsdk4.NewNetworkBuilder().
				Id(network.MustId()).
				MustBuild(),
		).
		VnicProfile(
			ovirtsdk4.NewVnicProfileBuilder().
				Id(vnicProfile.MustId()).
				MustBuild(),
		).
		OnBoot(true).
		Linked(true)

	nic, err := nicBuilder.Build()
	if err != nil {
		return fmt.Errorf("Error creating NIC: %s", err)
	}

	_, err = nicsService.Add().Nic(nic).Send()
	if err != nil {
		return fmt.Errorf("Error adding NIC to VM: %s", err)
	}

	log.Printf("Successfully created and attached new network interface to network: %s", config.NetworkName)
	return nil
}

func (s *stepCreateVM) waitForVMReady(conn *ovirtsdk4.Connection, vmID string, state multistep.StateBag) error {
	vmStateChange := StateChangeConf{
		Pending:   []string{"image_locked"},
		Target:    []string{string(ovirtsdk4.VMSTATUS_DOWN)},
		Refresh:   VMStateRefreshFunc(conn, vmID),
		StepState: state,
	}
	if _, err := WaitForState(&vmStateChange); err != nil {
		return fmt.Errorf("Error waiting for VM to be ready: %s", err)
	}
	return nil
}

func (s *stepCreateVM) Cleanup(state multistep.StateBag) {
	config := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	conn := state.Get("conn").(*ovirtsdk4.Connection)

	vmID, ok := state.GetOk("vm_id")
	if !ok {
		return
	}

	// Check if VM is running and stop it
	vmResp, err := conn.SystemService().
		VmsService().
		VmService(vmID.(string)).
		Get().
		Send()
	if err != nil {
		ui.Error(fmt.Sprintf("Error getting VM status: %s", err))
		return
	}

	vm := vmResp.MustVm()
	vmStatus := string(vm.MustStatus())

	if vmStatus == string(ovirtsdk4.VMSTATUS_DOWN) {
		ui.Say(fmt.Sprintf("VM '%s' is already stopped", config.VMName))
	} else {
		ui.Say(fmt.Sprintf("Stopping VM '%s'...", config.VMName))
		_, err = conn.SystemService().
			VmsService().
			VmService(vmID.(string)).
			Stop().
			Send()
		if err != nil {
			ui.Error(fmt.Sprintf("Error stopping VM: %s", err))
			return
		}

		// Wait for VM to stop
		vmStateChange := StateChangeConf{
			Pending:   []string{string(ovirtsdk4.VMSTATUS_UP)},
			Target:    []string{string(ovirtsdk4.VMSTATUS_DOWN)},
			Refresh:   VMStateRefreshFunc(conn, vmID.(string)),
			StepState: state,
		}
		if _, err := WaitForState(&vmStateChange); err != nil {
			ui.Error(fmt.Sprintf("Error waiting for VM to stop: %s", err))
			return
		}
	}

	// Delete VM if cleanup is enabled
	if config.CleanupVM != nil && *config.CleanupVM {
		ui.Say(fmt.Sprintf("Deleting virtual machine: %s", config.VMName))
		_, err = conn.SystemService().
			VmsService().
			VmService(vmID.(string)).
			Remove().
			Send()
		if err != nil {
			ui.Error(fmt.Sprintf("Error deleting VM: %s", err))
		}
	} else {
		ui.Say(fmt.Sprintf("Skipping VM cleanup due to cleanup_vm setting. VM '%s' will remain in the system.", config.VMName))
	}
}
