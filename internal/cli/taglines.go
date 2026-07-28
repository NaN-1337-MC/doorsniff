package cli

import (
	"math/rand"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type TaglineEntry struct {
	Text   string `yaml:"text"`
	Weight int    `yaml:"weight"`
}

type TaglinesConfig struct {
	Taglines []TaglineEntry `yaml:"taglines"`
}

func randomTagline() string {
	configPath := filepath.Join(os.Getenv("PWD"), "internal", "cli", "taglines.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "a Minecraft server scanner."
	}

	var cfg TaglinesConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "a Minecraft server scanner."
	}

	if len(cfg.Taglines) == 0 {
		return "a Minecraft server scanner."
	}

	totalWeight := 0
	for _, t := range cfg.Taglines {
		totalWeight += t.Weight
	}

	if totalWeight == 0 {
		return "a Minecraft server scanner."
	}

	r := rand.Intn(totalWeight)
	cumulative := 0
	for _, t := range cfg.Taglines {
		cumulative += t.Weight
		if r < cumulative {
			return t.Text
		}
	}

	return cfg.Taglines[0].Text
}
