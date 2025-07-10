package olvm

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/packer"
	ovirtsdk4 "github.com/ovirt/go-ovirt"
)

type stepExportTemplateToOVA struct {
	Debug bool
}

// ExportRequest represents the JSON payload for the export API call
type ExportRequest struct {
	Host      HostInfo `json:"host"`
	Directory string   `json:"directory"`
	Filename  string   `json:"filename"`
}

type HostInfo struct {
	Name string `json:"name"`
}

func (s *stepExportTemplateToOVA) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	config := state.Get("config").(*Config)
	ui := state.Get("ui").(packer.Ui)
	connWrapper := state.Get("connWrapper").(*ConnectionWrapper)

	// Skip if export_host is not set
	if config.ExportHost == "" {
		return multistep.ActionContinue
	}

	templateID, ok := state.GetOk("template_id")
	if !ok {
		ui.Error("No template ID found in state for export")
		return multistep.ActionHalt
	}

	ui.Say(fmt.Sprintf("Exporting template to OVA format on host %s...", config.ExportHost))

	// Get the Templates service
	var templatesService *ovirtsdk4.TemplatesService
	err := connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		templatesService = conn.SystemService().TemplatesService()
		return nil
	})
	if err != nil {
		err = fmt.Errorf("Error getting templates service: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	// Verify the template exists and is accessible
	ui.Message(fmt.Sprintf("Verifying template %s exists and is accessible...", templateID.(string)))

	var templateResp *ovirtsdk4.TemplateServiceGetResponse
	err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		templateResp, err = templatesService.TemplateService(templateID.(string)).Get().Send()
		return err
	})

	if err != nil {
		err = fmt.Errorf("Error accessing template %s: %s", templateID.(string), err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	template := templateResp.MustTemplate()

	ui.Message(fmt.Sprintf("Template %s is accessible (Status: %s)", template.MustName(), template.MustStatus()))

	// Verify the host exists before attempting export
	ui.Message(fmt.Sprintf("Verifying host %s exists...", config.ExportHost))

	var hostsService *ovirtsdk4.HostsService
	err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		hostsService = conn.SystemService().HostsService()
		return nil
	})
	if err != nil {
		err = fmt.Errorf("Error getting hosts service: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	var hostsResp *ovirtsdk4.HostsServiceListResponse
	err = connWrapper.ExecuteWithReconnect(func(conn *ovirtsdk4.Connection) error {
		var err error
		hostsResp, err = hostsService.List().Search(fmt.Sprintf("name=%s", config.ExportHost)).Send()
		return err
	})

	if err != nil {
		err = fmt.Errorf("Error searching for host %s: %s", config.ExportHost, err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	hosts, ok := hostsResp.Hosts()
	if !ok || len(hosts.Slice()) == 0 {
		err = fmt.Errorf("Host %s not found in OLVM", config.ExportHost)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	host := hosts.Slice()[0]
	ui.Message(fmt.Sprintf("Found host %s (ID: %s, Status: %s)",
		host.MustName(), host.MustId(), host.MustStatus()))

	// Make direct HTTP API call to export the template
	ui.Message("Calling OLVM API to export template...")

	// Build the export request payload
	exportReq := ExportRequest{
		Host: HostInfo{
			Name: host.MustName(),
		},
		Directory: config.ExportDirectory,
		Filename:  config.ExportFileName,
	}

	// Convert to JSON
	jsonData, err := json.Marshal(exportReq)
	if err != nil {
		err = fmt.Errorf("Error marshaling export request: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	// Build the API URL
	apiURL := fmt.Sprintf("%s/templates/%s/export",
		config.AccessConfig.olvmParsedURL.String(), templateID.(string))

	// Create HTTP client with TLS skip if configured
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: config.AccessConfig.TLSInsecure,
			},
		},
	}

	// Create the HTTP request
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		err = fmt.Errorf("Error creating HTTP request: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Basic %s", config.AccessConfig.getBasicAuth()))

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		err = fmt.Errorf("Error making HTTP request: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("Error reading response body: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	// Check response status
	if resp.StatusCode >= 400 {
		err = fmt.Errorf("Export API call failed with status %s: %s", resp.Status, string(body))
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	ui.Say(fmt.Sprintf("Successfully initiated template export to OVA: %s/%s on host %s",
		config.ExportDirectory, config.ExportFileName, config.ExportHost))

	// Poll for export completion (template status returns to 'ok')
	ui.Message("Waiting for template export to complete (status returns to 'ok')...")
	templateStateChange := StateChangeConf{
		Pending:   []string{"locked", "image_locked"},
		Target:    []string{"ok"},
		Refresh:   TemplateStateRefreshFuncWithWrapper(connWrapper, templateID.(string)),
		StepState: state,
	}
	if _, err := WaitForState(&templateStateChange); err != nil {
		err := fmt.Errorf("Error waiting for template export to complete: %s", err)
		ui.Error(err.Error())
		state.Put("error", err)
		return multistep.ActionHalt
	}

	ui.Say(fmt.Sprintf("Template export to OVA completed: %s/%s on host %s",
		config.ExportDirectory, config.ExportFileName, config.ExportHost))

	return multistep.ActionContinue
}

func (s *stepExportTemplateToOVA) Cleanup(state multistep.StateBag) {
	// Nothing to cleanup for this step
}
