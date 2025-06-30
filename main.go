package main

import (
	"fmt"
	"os"

	"github.com/mikelaws/packer-plugin-olvm/builder/olvm"
	"github.com/mikelaws/packer-plugin-olvm/version"
	"github.com/hashicorp/packer-plugin-sdk/plugin"
)

func main() {
	pps := plugin.NewSet()
	pps.RegisterBuilder(plugin.DEFAULT_NAME, new(olvm.Builder))
	pps.SetVersion(version.PluginVersion)

	if err := pps.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
