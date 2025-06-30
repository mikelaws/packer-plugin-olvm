package olvm

import (
	"github.com/hashicorp/packer-plugin-sdk/multistep"
)

func commHost(state multistep.StateBag) (string, error) {
	c := state.Get("config").(*Config)
	return c.IPAddress, nil
}
