package core

import (
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// Config holds the plugin-owned settings parsed from plugins.configs.warp.
type Config struct {
	Enabled        bool   `yaml:"enabled"`
	Priority       int    `yaml:"priority"`
	UseWarpCredits bool   `yaml:"use_warp_credits"`
	ModelPrefix    string `yaml:"model_prefix"`
	ClientVersion  string `yaml:"client_version"`
	OSCategory     string `yaml:"os_category"`
	OSName         string `yaml:"os_name"`
	OSVersion      string `yaml:"os_version"`
}

func defaultConfig() Config {
	return Config{
		Enabled:        true,
		UseWarpCredits: true,
		ModelPrefix:    "warp/",
		ClientVersion:  "v0.2025.08.06.08.12.stable_02",
		OSCategory:     "Windows",
		OSName:         "Windows",
		OSVersion:      "11 (26100)",
	}
}

var currentConfig atomic.Value // Config

func init() { currentConfig.Store(defaultConfig()) }

// CurrentConfig returns the last-applied plugin configuration.
func CurrentConfig() Config { return currentConfig.Load().(Config) }

// applyConfigYAML parses plugin config_yaml bytes over the defaults.
func applyConfigYAML(raw []byte) error {
	cfg := defaultConfig()
	if len(raw) > 0 {
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return err
		}
	}
	if cfg.ModelPrefix == "" {
		cfg.ModelPrefix = "warp/"
	}
	currentConfig.Store(cfg)
	return nil
}
