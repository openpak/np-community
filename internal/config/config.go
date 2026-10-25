package config

import "fmt"

type Config struct{ CoreAddress, CoreKey string }

func Load(getenv func(string) string) (*Config, error) {
	c := &Config{CoreAddress: getenv("NP_CORE_ADDR"), CoreKey: getenv("NP_CORE_KEY")}
	if c.CoreAddress == "" {
		c.CoreAddress = "localhost:7000"
	}
	if len(c.CoreKey) < 32 {
		return nil, fmt.Errorf("NP_CORE_KEY must be at least 32 bytes")
	}
	return c, nil
}
