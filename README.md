# Packer Plugin for Oracle Linux Virtualization Manager (OLVM)

This plugin for HashiCorp [Packer][packer-link] provides a builder for Oracle [OLVM][olvm-link] to create customized VM templates from either source templates, or source disk images. This may include, for example, [Official Oracle Enterprise Linux OVA Templates][oel-link], or compatible cloud disk images provided by many Linux distributions (Ubuntu, Debian, RockyLinux, CentOS, etc.)

## Features

- VM template creation from either source templates or source disk images
- Support for Packer standard communicators and provisioners
- Optionally export template artifacts (in OVA format) for distribution
- Ability to troubleshoot build issues by disabling VM cleanup/deletion
- Configurable networking and OS network interface name
- Configurable storage interface (`virtio-scsi`, `virtio`)

## Installation

### Prerequisites

- Packer >= v1.10.2
- Access to an OLVM environment
- Access/connectivity to the OLVM API

### Using Pre-Built Releases

#### Using `packer init` (Preferred)

The `packer init` command automatically installs any required Packer plugins defined in your configuration. To install this plugin, copy and paste this code into your Packer configuration, then run [`packer init`][packer-init-link].

```hcl
packer {
  required_plugins {
    olvm = {
      version = ">= 1.0.3"
      source  = "github.com/mikelaws/olvm"
    }
  }
}
```

#### Using `packer plugins` (Automatic)

Packer plugins may be installed automatically using the `packer plugins` command. Simply run the following command to automatically download and install the plugin, or see the [Packer documentation][packer-plugins-link] for more details.

```sh
$ packer plugins install github.com/mikelaws/olvm
```

#### Using `packer plugins` (Manual)

Packer plugins may be installed manually using the `packer plugins` command. Please find the binary release of the OLVM plugin for your operating system/platform [here][release-link], then uncompress the archive to find the pre-built binary file. Install the plugin binary file using the steps outlined in the [Packer documentation][packer-plugins-link].

### Build From Source (Advanced)

If you prefer to build the plugin from source, clone the GitHub repository locally and run the command `make build` from the root of the repository tree. Upon successful compilation, the `packer-plugin-olvm` plugin binary file can be found in the `bin/` directory. To install the compiled plugin, please follow the [Packer plugins documentation][packer-plugins-link].

```bash
git clone https://github.com/mikelaws/packer-plugin-olvm.git
cd packer-plugin-olvm
make build
```

### Configuration

For more information on how to configure the plugin, please reference the documentation located in the [`docs/`](docs) directory.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Contributing

* __Support/Bug Reports__ - If you think you've found a bug, or you hit a snag and need some help, please [open an issue][issue-link] in this repository.
* __Code Contributions__ - Contributions are also welcome! If you have an idea for a feature, or want to fix a bug, please start by opening an issue to capture the conversation, then [fork this repository][fork-link], push your changes to your fork, and finally open a Pull Request in this repository.

[olvm-link]: https://docs.oracle.com/en/virtualization/oracle-linux-virtualization-manager/
[oel-link]: https://yum.oracle.com/oracle-linux-templates.html
[packer-link]: https://www.packer.io/
[packer-plugins-link]: https://www.packer.io/docs/extending/plugins/#installing-plugins
[packer-init-link]: https://www.packer.io/docs/commands/init
[release-link]: https://github.com/mikelaws/packer-plugin-olvm/releases
[issue-link]: https://github.com/mikelaws/packer-plugin-olvm/issues
[fork-link]: https://github.com/mikelaws/packer-plugin-olvm#fork-destination-box
