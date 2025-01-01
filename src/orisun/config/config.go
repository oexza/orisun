package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// AppConfig represents the application configuration
type AppConfig struct {
	DB   DBConfig
	Grpc struct {
		Port             string
		EnableReflection bool
	}
	PollingPublisher struct {
		BatchSize int32
	}
	Logging struct {
		Enabled bool
		Level   string // e.g., "debug", "info", "warn", "error"
	}
	Nats struct {
		Port           int
		MaxPayload     int32
		MaxConnections int
		StoreDir       string
		Cluster        NatsClusterConfig
	}
	// Prod bool
	Auth struct {
		AdminUsername string
		AdminPassword string
	}
	Admin struct {
		Port    string
		Schema  string
	}
}

type DBConfig struct {
	User     string
	Name     string
	Password string
	Host     string
	Port     string
	Schemas  string
}

func (c *DBConfig) GetSchemas() []string {
	return strings.Split(c.Schemas, ",")
}

type NatsClusterConfig struct {
	Name     string
	Host     string
	Port     int
	Routes   string
	Username string
	Password string
	Enabled  bool
	Timeout  time.Duration
}

func (c *NatsClusterConfig) GetRoutes() []string {
	return strings.Split(c.Routes, ",")
}

//go:embed config.yaml
var configData []byte

func LoadConfig() (*AppConfig, error) {
	viper.SetConfigType("yaml") // Set the type of the config file

	// Read from the embedded config data
	if err := viper.ReadConfig(bytes.NewReader(configData)); err != nil {
		return nil, fmt.Errorf("failed to read config data: %w", err)
	}

	// Custom environment variable substitution
	for _, key := range viper.AllKeys() {
		value := viper.GetString(key)
		viper.Set(key, substituteEnvVars(value))
	}

	var config AppConfig
	err := viper.Unmarshal(&config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &config, nil
}

func substituteEnvVars(value string) string {
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
		parts := strings.SplitN(value[2:len(value)-1], ":", 2)
		envVar := parts[0]
		defaultValue := ""
		if len(parts) > 1 {
			defaultValue = parts[1]
		}

		if envValue := os.Getenv(envVar); envValue != "" {
			return envValue
		}
		return defaultValue
	}
	return value
}
