package common

import (
	"fmt"
	"os"
	"strconv"

	"github.com/mediocregopher/radix/v3"
)

const (
	defaultRedisHost = "127.0.0.1"
	defaultRedisPort = 6379
)

type RedisConfig struct {
	Host string
	Port int
}

func NewRedisConfig() RedisConfig {
	port := defaultRedisPort
	if value := os.Getenv("REDIS_PORT"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			port = parsed
		}
	}
	return RedisConfig{
		Host: envOrDefault("REDIS_HOST", defaultRedisHost),
		Port: port,
	}
}

func (c RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func GetRedisPool() (*radix.Pool, error) {
	return GetRedisPoolByconfig(NewRedisConfig())
}

func GetRedisPoolByconfig(config RedisConfig) (*radix.Pool, error) {
	pool, err := radix.NewPool("tcp", config.Addr(), 10)
	if err != nil {
		return nil, err
	}
	return pool, nil
}
