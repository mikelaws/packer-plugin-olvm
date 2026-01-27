# OLVM Builder Configuration Reference

## Example Usage

### Template-based Build

```hcl
source "olvm" "template-example" {
  # OLVM Configuration
  olvm_url = "https://olvm.example.com/ovirt-engine/api"
  username = "admin@internal"
  password = "password"
  tls_insecure = true

  # Source Configuration
  source_template_name = "oracle-linux-8-template"
  cluster = "Default"

  # Network Configuration
  network_name = "ovirtmgmt"  # Optional (defaults to "ovirtmgmt")
  vnic_profile = "ovirtmgmt"
  dns_servers = ["8.8.8.8", "8.8.4.4"]
  os_interface_name = "ens3"  # For systems using predictable network interface names

  # VM Configuration
  vm_name = "packer-test-vm"
  # vm_vcpu_count and vm_memory_mb are optional (default to 1 CPU, 1024MB)
  vm_vcpu_count = 2
  vm_memory_mb = 4096

  # SSH Configuration
  ssh_username = "root"
  ssh_timeout = "30m"

  # Template Configuration
  destination_template_name = "my-custom-template"
  destination_template_description = "Template created by Packer"

  # Cleanup Configuration
  cleanup_vm = true
  cleanup_interfaces = true
}

build {
  sources = ["source.olvm.template-example"]
}
```

### Disk-based Build

```hcl
source "olvm" "disk-example" {
  # OLVM Configuration
  olvm_url = "https://olvm.example.com/ovirt-engine/api"
  username = "admin@internal"
  password = "password"
  tls_insecure = true

  # Source Configuration
  source_disk_name = "ubuntu-22.04-cloud-disk"
  cluster = "Default"

  # Network Configuration
  network_name = "ovirtmgmt"  # Optional (defaults to "ovirtmgmt")
  vnic_profile = "ovirtmgmt"
  dns_servers = ["8.8.8.8", "8.8.4.4"]
  os_interface_name = "ens3"

  # VM Configuration
  vm_name = "packer-ubuntu-vm"
  # vm_vcpu_count and vm_memory_mb are optional (default to 1 CPU, 1024MB)
  vm_vcpu_count = 2
  vm_memory_mb = 4096
  vm_storage_driver = "virtio-scsi"

  # SSH Configuration
  ssh_username = "ubuntu"
  ssh_timeout = "30m"

  # Template Configuration
  destination_template_name = "ubuntu-22.04-template"

  # Cleanup Configuration
  cleanup_vm = true
  cleanup_interfaces = true
}

build {
  sources = ["source.olvm.disk-example"]
}
```

### URL-based Disk Build

```hcl
source "olvm" "url-disk-example" {
  # OLVM Configuration
  olvm_url = "https://olvm.example.com/ovirt-engine/api"
  username = "admin@internal"
  password = "password"
  tls_insecure = true

  # Source Configuration - Remote URL disk image
  source_disk_url = "https://cloud-images.ubuntu.com/releases/22.04/release/ubuntu-22.04-server-cloudimg-amd64.img"
  source_disk_checksum_url = "https://cloud-images.ubuntu.com/releases/22.04/release/SHA256SUMS"
  source_disk_url_checksum_type = "sha256"
  source_disk_storage_domain = "example_iscsi_block"  # Optional: specify storage domain
  source_disk_upload_name = "ubuntu-22.04-amd64.img"  # Optional: defaults to filename from URL
  cluster = "Default"

  # Network Configuration
  network_name = "ovirtmgmt"
  vnic_profile = "ovirtmgmt"
  dns_servers = ["8.8.8.8", "8.8.4.4"]
  os_interface_name = "ens3"

  # VM Configuration
  vm_name = "packer-ubuntu-url-vm"
  vm_vcpu_count = 2
  vm_memory_mb = 4096
  vm_storage_driver = "virtio-scsi"
  vm_firmware_type = "bios"  # Optional: "bios" or "uefi" (defaults to cluster default)

  # SSH Configuration
  ssh_username = "ubuntu"
  ssh_timeout = "30m"

  # Template Configuration
  destination_template_name = "ubuntu-22.04-url-template"

  # Cleanup Configuration
  cleanup_vm = true
  cleanup_interfaces = true
}

build {
  sources = ["source.olvm.url-disk-example"]
}
```

**Alternative URL example with direct checksum and RAW conversion:**

```hcl
source "olvm" "url-disk-raw-example" {
  # OLVM Configuration
  olvm_url = "https://olvm.example.com/ovirt-engine/api"
  username = "admin@internal"
  password = "password"
  tls_insecure = true

  # Source Configuration - RAW image with direct checksum
  source_disk_url = "https://example.com/images/ubuntu-22.04-server-cloudimg-amd64.img"
  source_disk_url_checksum = "f5d311aad28742200fabb183a8af42292ad4f22c941a4371b736c82089bf67ee"
  source_disk_url_checksum_type = "sha256"
  source_disk_storage_domain = "example_iscsi_block"
  convert_raw_sparse_to_preallocated = true  # Required for RAW on block storage
  cluster = "Default"

  # VM Configuration
  vm_name = "packer-ubuntu-raw-vm"
  vm_firmware_type = "bios"  # Some cloud images require BIOS, not UEFI
  # ... rest of configuration ...
}
```

**Notes:** 
- The plugin automatically detects the image format (qcow2 or raw) by reading file headers, regardless of file extension.
- For RAW images on block storage (iSCSI/FCP), you may need to enable `convert_raw_sparse_to_preallocated = true` to convert sparse RAW images to preallocated format, as sparse RAW images are incompatible with block storage domains.
- Checksums are automatically stored in the disk description using the format `[packer-checksum:algorithm:hash]` for duplicate detection.

## Configuration Options

The OLVM builder supports the following parameters:

### Required Configuration

#### OLVM Configuration

- `olvm_url` - The URL of the OLVM API endpoint
- `username` - Username for OLVM authentication
- `password` - Password for OLVM authentication

#### Source Configuration (any one of the following)

**Template-based sources:**
- `source_template_name` - Name of the source template
- `source_template_id` - ID of the source template (alternative to source_template_name)

**Existing disk-based sources:**
- `source_disk_name` - Name of the source disk image
- `source_disk_id` - ID of the source disk image (alternative to source_disk_name)

**Remote URL-based sources:**
- `source_disk_url` - HTTP/HTTPS URL to a remote disk image (qcow2, raw, img formats supported)
- `source_disk_checksum_url` - Optional URL to a checksum file for validation (supports standard and BSD-style checksum file formats)
- `source_disk_url_checksum` - Optional direct checksum value for validation
- `source_disk_url_checksum_type` - Checksum algorithm: `md5`, `sha1`, `sha256`, or `sha512` (defaults to `sha256` if checksum is provided)
- `source_disk_upload_name` - Optional name for the uploaded disk (defaults to filename from URL)
- `source_disk_storage_domain` - Optional storage domain name for the uploaded disk (defaults to cluster default)
- `force_upload` - Force upload even if duplicate disk exists (defaults to false)
- `convert_raw_sparse_to_preallocated` - Convert RAW sparse images to preallocated format for block storage compatibility (defaults to false)

> **Note:** OVA template files are not supported via URL. Only disk image formats (qcow2, raw, img) can be downloaded from URLs.

> **Duplicate Detection:** When using URL-based sources, the plugin automatically checks for existing disks with the same name and checksum before downloading. If a matching disk is found (by name and checksum stored in the disk description), the existing disk is reused to prevent storage domain bloat. If a disk with the same name exists but has a different checksum, the build will fail unless `force_upload = true` is set.

### Optional Configuration

#### OLVM Configuration

- `tls_insecure` - Skip TLS verification (defaults to false)
- `max_retries` - Maximum number of retry attempts for communication issues (defaults to 4)
- `retry_interval_sec` - Interval between retry attempts in seconds (defaults to 2)

#### Source Configuration

- `source_template_version` - Version of the source template (defaults to 1)
- `cluster` - OLVM cluster name (defaults to "Default")

#### VM Configuration

- `vm_name` - Name for the VM (defaults to "packer-<time-ordered-uuid>")
- `vm_vcpu_count` - Number of virtual CPUs (defaults to 1)
- `vm_memory_mb` - Memory in MB (defaults to 1024)
- `vm_storage_driver` - Storage interface type: `virtio-scsi` or `virtio` (defaults to "virtio-scsi")
- `vm_firmware_type` - VM firmware type: `bios` or `uefi` (defaults to cluster default if unset)
  - `bios` - Sets "Q35 Chipset with BIOS" (compatible with most traditional OS images)
  - `uefi` - Sets "Q35 Chipset with UEFI" (required for modern UEFI-based OS images)

#### Network Configuration

- `network_name` - Name of the OLVM network to attach to the VM (defaults to "ovirtmgmt")
- `vnic_profile` - vNIC profile to use for the network interface (defaults to `network_name` if not specified)
- `dns_servers` - List of DNS server IP addresses
- `os_interface_name` - Operating system network interface name (defaults to "eth0")
- `address` - Static IP address for the VM
- `netmask` - Network mask (defaults to "255.255.255.0")
- `gateway` - Gateway address

> **Note:** For template-based builds, if the source template already has network interfaces configured, the plugin will configure the first existing interface with the specified `network_name` and `vnic_profile`. If no network interfaces exist, a new one will be created. For disk-based builds, a new network interface is always created.

#### Template Creation

- `destination_template_name` - Name for the generated template (optional)
- `destination_template_description` - Description for the template. Defaults to "Template created by Packer from VM <vm_name>".
- `template_seal` - Whether to seal the template during creation (defaults to true)

#### Cleanup Configuration

- `cleanup_vm` - Whether to delete the VM after template creation (defaults to true)
- `cleanup_interfaces` - Whether to remove network interfaces before template creation (defaults to true)

#### Export Configuration

- `export_host` - Host to export the template to
- `export_directory` - Directory on the export host to save the template (defaults to "/tmp")
- `export_file_name` - Filename for the exported OVA file (defaults to "<destination_template_name>.ova")

#### SSH Configuration

- `ssh_username` - SSH username
- `ssh_timeout` - SSH connection timeout (defaults to 5m)
- `ssh_handshake_attempts` - Number of SSH handshake attempts (defaults to 10)

#### TLS Configuration

- `tls_insecure` - Skip TLS verification (defaults to false)

## Environment Variables

The following environment variables can be used instead of configuration options:

- `OLVM_URL` - OLVM API URL
- `OLVM_USERNAME` - OLVM username
- `OLVM_PASSWORD` - OLVM password

## Packer Compatibility

This plugin is compatible with Packer >= v1.10.2
