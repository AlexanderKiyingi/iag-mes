package config

import (
	"os"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	Port          string
	ServiceName   string
	KafkaBrokers  []string
	KafkaTopic    string
	KafkaClientID string
}

func Load() Config {
	_ = godotenv.Load()
	return Config{
		Port:          getenv("PORT", "4003"),
		ServiceName:   getenv("SERVICE_NAME", "mes"),
		KafkaBrokers:  splitCSV(getenv("KAFKA_BROKERS", "")),
		KafkaTopic:    getenv("KAFKA_PRODUCTION_TOPIC", "iag.production"),
		KafkaClientID: getenv("KAFKA_CLIENT_ID", "iag-mes"),
	}
}

func getenv(k, d string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return d
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
