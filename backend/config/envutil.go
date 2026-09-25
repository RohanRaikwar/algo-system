package config

import (
	"os"
	"strconv"
	"strings"
)

// GetEnv returns the value of an environment variable, or fallback if unset/empty.
func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// GetEnvInt returns the integer value of an environment variable, or fallback on error.
func GetEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

// GetEnvInt64 returns the int64 value of an environment variable, or fallback on error.
func GetEnvInt64(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

// GetEnvBool returns true if the environment variable is "true" (case-insensitive).
func GetEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		if fallback {
			return true
		}
		return false
	}
	return strings.EqualFold(v, "true")
}
