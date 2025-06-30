// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package olvm

import (
	"fmt"
	"log"
)

// Artifact is an artifact implementation that contains built template.
type Artifact struct {
	templateID string
}

// BuilderId uniquely identifies the builder.
func (*Artifact) BuilderId() string {
	return BuilderID
}

// Files returns the files represented by the artifact. Not used for OLVM.
func (*Artifact) Files() []string {
	return nil
}

// Id returns the template identifier of the artifact.
func (a *Artifact) Id() string {
	return a.templateID
}

func (a *Artifact) String() string {
	return fmt.Sprintf("A template was created: %s", a.templateID)
}

// State returns specific details from the artifact. Not used for OLVM.
func (a *Artifact) State(name string) interface{} {
	return nil
}

// Destroy deletes the template associated with the artifact.
func (a *Artifact) Destroy() error {
	log.Printf("Destroying template: %s", a.templateID)
	//TODO: Implement template deletion logic if needed
	return nil
}
