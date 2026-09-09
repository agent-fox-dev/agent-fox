package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newCampaignCmd creates the "spec campaign" subcommand which creates
// a named campaign to group related specs.
func newCampaignCmd() *cobra.Command {
	var path string
	var name string
	var description string

	cmd := &cobra.Command{
		Use:   "campaign",
		Short: "Create a named campaign to group related specs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreateCampaign(cmd, path, name, description)
		},
	}

	cmd.Flags().StringVarP(&path, "path", "p", "", "path for the campaign directory")
	cmd.Flags().StringVarP(&name, "name", "n", "", "name for the campaign")
	cmd.Flags().StringVar(&description, "description", "", "description for the campaign")

	// --path and --name are required.
	cmd.MarkFlagRequired("path")
	cmd.MarkFlagRequired("name")

	return cmd
}

// runCreateCampaign creates a campaign at the given path with the given
// name and optional description. It writes campaign.yaml atomically and
// prints a confirmation message to stderr on success.
//
// Returns an error (CampaignError semantics) if campaign.yaml already
// exists at the path.
func runCreateCampaign(cmd *cobra.Command, path, name, description string) error {
	// Check if campaign.yaml already exists at the target path.
	yamlPath := filepath.Join(path, "campaign.yaml")
	if _, err := os.Stat(yamlPath); err == nil {
		return fmt.Errorf("campaign already exists at %q: %s already contains a campaign.yaml", path, path)
	}

	// Create the campaign directory if it doesn't exist.
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("cannot create campaign directory %q: %w", path, err)
	}

	// Build campaign.yaml content.
	content := fmt.Sprintf("name: %s\n", name)
	if description != "" {
		content += fmt.Sprintf("description: %s\n", description)
	}

	// Write campaign.yaml atomically.
	if err := os.WriteFile(yamlPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("cannot write campaign.yaml: %w", err)
	}

	// Print confirmation message to stderr.
	fmt.Fprintf(cmd.ErrOrStderr(), "Campaign %q created at %s\n", name, path)
	return nil
}
