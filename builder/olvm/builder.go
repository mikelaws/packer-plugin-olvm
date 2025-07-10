//go:generate packer-sdc mapstructure-to-hcl2 -type Config

package olvm

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer-plugin-sdk/communicator"
	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/multistep/commonsteps"
	"github.com/hashicorp/packer-plugin-sdk/packer"
)

const BuilderID = "olvm"

type Builder struct {
	config Config
	runner multistep.Runner
}

func (b *Builder) ConfigSpec() hcldec.ObjectSpec { return b.config.FlatMapstructure().HCL2Spec() }

func (b *Builder) Prepare(raws ...interface{}) (generatedVars []string, warnings []string, err error) {
	c, warnings, errs := NewConfig(raws...)
	if errs != nil {
		return nil, warnings, errs
	}
	b.config = *c

	// Generate default destination template name if not specified
	if b.config.DestinationTemplateName == "" {
		epochTimestamp := strconv.FormatInt(time.Now().Unix(), 10)
		var baseName string
		switch {
		case b.config.SourceTemplateName != "":
			baseName = b.config.SourceTemplateName
		case b.config.SourceDiskName != "":
			baseName = b.config.SourceDiskName
		default:
			baseName = "olvm"
		}
		b.config.DestinationTemplateName = fmt.Sprintf("packer-%s-%s", baseName, epochTimestamp)
		log.Printf("Generated destination template name: %s", b.config.DestinationTemplateName)
	}

	// Generate default destination template description if not specified
	if b.config.DestinationTemplateDescription == "" {
		b.config.DestinationTemplateDescription = fmt.Sprintf("Template created by Packer from VM %s", b.config.VMName)
		log.Printf("Generated destination template description: %s", b.config.DestinationTemplateDescription)
	}

	// Set default value for cleanup_interfaces if not specified
	if !b.config.CleanupInterfaces {
		b.config.CleanupInterfaces = true
		log.Printf("Using default cleanup_interfaces: %t", b.config.CleanupInterfaces)
	}

	// Set default value for cleanup_vm if not specified
	if b.config.CleanupVM == nil {
		defaultCleanupVM := true
		b.config.CleanupVM = &defaultCleanupVM
		log.Printf("Using default cleanup_vm: %t", *b.config.CleanupVM)
	}

	// Set default export filename if export_host is specified but filename is not
	if b.config.ExportHost != "" && b.config.ExportFileName == "" {
		b.config.ExportFileName = fmt.Sprintf("%s.ova", b.config.DestinationTemplateName)
		log.Printf("Using default export_file_name: %s", b.config.ExportFileName)
	}

	return nil, warnings, nil
}

func (b *Builder) Run(ctx context.Context, ui packer.Ui, hook packer.Hook) (packer.Artifact, error) {
	var err error

	// Create connection wrapper instead of direct connection
	connWrapper, err := NewConnectionWrapper(&b.config, ui)
	if err != nil {
		return nil, err
	}
	defer connWrapper.Close()

	// Get the initial connection to verify it works
	conn, err := connWrapper.GetConnection()
	if err != nil {
		return nil, err
	}

	log.Printf("Successfully connected to %s\n", b.config.AccessConfig.olvmParsedURL.String())

	state := new(multistep.BasicStateBag)
	state.Put("config", &b.config)
	state.Put("conn", conn)
	state.Put("connWrapper", connWrapper) // Store the wrapper for steps that need it
	state.Put("hook", hook)
	state.Put("ui", ui)

	steps := []multistep.Step{}
	steps = append(steps, &stepKeyPair{
		Debug:        b.config.PackerDebug,
		Comm:         &b.config.Comm,
		DebugKeyPath: fmt.Sprintf("olvm_%s.pem", b.config.PackerBuildName),
	})
	steps = append(steps, &stepCreateVM{
		Ctx:   b.config.ctx,
		Debug: b.config.PackerDebug,
	})
	steps = append(steps, &stepSetupInitialRun{
		Debug: b.config.PackerDebug,
		Comm:  &b.config.Comm,
	})
	steps = append(steps, &communicator.StepConnect{
		Config:    &b.config.Comm,
		Host:      commHost,
		SSHConfig: b.config.Comm.SSHConfigFunc(),
	})
	steps = append(steps, &commonsteps.StepProvision{})
	steps = append(steps, &commonsteps.StepCleanupTempKeys{
		Comm: &b.config.Comm,
	})
	steps = append(steps, &stepStopVM{})
	steps = append(steps, &stepCleanupInterfaces{})
	steps = append(steps, &stepCreateTemplateFromVM{
		Debug: b.config.PackerDebug,
	})
	steps = append(steps, &stepExportTemplateToOVA{
		Debug: b.config.PackerDebug,
	})

	defer func() {
		if err := recover(); err != nil {
			fmt.Printf("OLVM: Panic occurred, try the non-Must methods to find the reason (%s)", err)
		}
	}()

	b.runner = commonsteps.NewRunner(steps, b.config.PackerConfig, ui)
	b.runner.Run(ctx, state)

	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}

	// Check if template was created successfully
	templateID, ok := state.GetOk("template_id")
	if !ok {
		return nil, nil
	}

	artifact := &Artifact{
		templateID: templateID.(string),
	}

	return artifact, nil
}
