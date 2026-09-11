package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseDSN     string `mapstructure:"DATABASE_DSN"`
	ServerAddress   string `mapstructure:"SERVER_ADDRESS"`
	ServerPort      int    `mapstructure:"SERVER_PORT"`
	JWTKey          string `mapstructure:"JWT_KEY"`
	LogLevel        string `mapstructure:"LOG_LEVEL"`
	SnowflakeNodeID int64  `mapstructure:"SNOWFLAKE_NODE_ID"`
	HasherWorkers   int    `mapstructure:"HASHER_WORKERS"`
	ChunkStorageDir string `mapstructure:"CHUNK_STORAGE_DIR"`
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	v.SetConfigFile(".env")
	v.SetConfigType("env")
	_ = v.MergeInConfig()

	v.AutomaticEnv()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to parse config: %w", err)
	}

	return &cfg, nil
}
