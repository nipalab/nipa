package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseDSN            string `mapstructure:"DATABASE_DSN"`
	ServerAddress          string `mapstructure:"SERVER_ADDRESS"`
	ServerPort             int    `mapstructure:"SERVER_PORT"`
	JWTKey                 string `mapstructure:"JWT_KEY"`
	LogLevel               string `mapstructure:"LOG_LEVEL"`
	SnowflakeNodeID        int64  `mapstructure:"SNOWFLAKE_NODE_ID"`
	HasherWorkers          int    `mapstructure:"HASHER_WORKERS"`
	ChunkStorageDir        string `mapstructure:"CHUNK_STORAGE_DIR"`
	ChunkURLSigningKey     string `mapstructure:"CHUNK_URL_SIGNING_KEY"`
	ChunkPresignTTLSeconds int    `mapstructure:"CHUNK_PRESIGN_TTL_SECONDS"`
	ChunkMaxPageSize       int    `mapstructure:"CHUNK_MAX_PAGE_SIZE"`
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	v.SetDefault("CHUNK_PRESIGN_TTL_SECONDS", 3600)
	v.SetDefault("CHUNK_MAX_PAGE_SIZE", 1000)

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
