package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/almaz-uno/qws/internal/config"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage QWS configuration",
	Long:  `Commands for managing QWS configuration files and settings.`,
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create default configuration file",
	Long: `Create a default configuration file at ~/.config/qws/config.yaml.
If the file already exists, it will not be overwritten unless --force is used.`,
	RunE: runConfigInit,
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	Long:  `Display the current configuration including values from file, environment variables, and defaults.`,
	RunE:  runConfigShow,
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show configuration file path",
	Long:  `Display the path to the configuration file being used, if any.`,
	RunE:  runConfigPath,
}

var (
	configForce bool
)

func init() {
	configInitCmd.Flags().BoolVarP(&configForce, "force", "f", false, "overwrite existing config file")

	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configPathCmd)

	rootCmd.AddCommand(configCmd)
}

func runConfigInit(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(home, ".config", "qws")
	configPath := filepath.Join(configDir, "config.yaml")

	// Check if file exists
	if !configForce {
		if _, err := os.Stat(configPath); err == nil {
			return fmt.Errorf("config file already exists at %s (use --force to overwrite)", configPath)
		}
	}

	// Create config directory
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Get default config
	defaultCfg := config.Default()

	// Marshal to YAML
	data, err := yaml.Marshal(defaultCfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write to file
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Printf("Configuration file created at: %s\n", configPath)
	return nil
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	// Initialize config to load current configuration
	initConfig()

	// Marshal current config to YAML
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	fmt.Println("Current configuration:")
	fmt.Println()
	fmt.Print(string(data))

	return nil
}

func runConfigPath(cmd *cobra.Command, args []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	var configPath string
	if cfgFile != "" {
		configPath = cfgFile
	} else {
		configPath = filepath.Join(home, ".config", "qws", "config.yaml")
	}

	// Check if file exists
	if _, err := os.Stat(configPath); err == nil {
		fmt.Printf("Config file: %s (exists)\n", configPath)
	} else {
		fmt.Printf("Config file: %s (not found, using defaults)\n", configPath)
		log.Debug().Str("path", configPath).Msg("Config file does not exist")
	}

	return nil
}
