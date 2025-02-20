/*
Copyright 2022 Red Hat Inc.
SPDX-License-Identifier: Apache-2.0
*/
package config

import (
	"fmt"
	"os"
	"strings"
	"sync"

	clowder "github.com/redhatinsights/app-common-go/pkg/api/v1"
	"github.com/spf13/viper"
)

const ExportTopic string = "platform.export.requests"

// ExportConfig represents the runtime configuration
type ExportConfig struct {
	Hostname           string
	PublicPort         int
	MetricsPort        int
	PrivatePort        int
	Logging            *loggingConfig
	LogLevel           string
	Debug              bool
	DBConfig           dbConfig
	StorageConfig      storageConfig
	KafkaConfig        kafkaConfig
	OpenAPIPrivatePath string
	OpenAPIPublicPath  string
	Psks               []string
	ExportExpiryDays   int
}

type dbConfig struct {
	User     string
	Password string
	Hostname string
	Port     string
	Name     string
	SSLCfg   dbSSLConfig
}

type dbSSLConfig struct {
	RdsCa   *string
	SSLMode string
}

type loggingConfig struct {
	AccessKeyID     string
	SecretAccessKey string
	LogGroup        string
	Region          string
}

type kafkaConfig struct {
	Brokers          []string
	GroupID          string
	ExportsTopic     string
	SSLConfig        kafkaSSLConfig
	EventSource      string
	EventSpecVersion string
	EventType        string
	EventDataSchema  string
}

type kafkaSSLConfig struct {
	CA            string
	Username      string
	Password      string
	SASLMechanism string
	Protocol      string
}

type storageConfig struct {
	Bucket    string
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

var config *ExportConfig
var doOnce sync.Once

// initialize the configuration for service
func Get() *ExportConfig {
	doOnce.Do(func() {
		options := viper.New()
		options.SetDefault("PUBLIC_PORT", 8000)
		options.SetDefault("METRICS_PORT", 9000)
		options.SetDefault("PRIVATE_PORT", 10000)
		options.SetDefault("LOG_LEVEL", "INFO")
		options.SetDefault("DEBUG", false)
		options.SetDefault("OPEN_API_FILE_PATH", "./static/spec/openapi.json")
		options.SetDefault("OPEN_API_PRIVATE_PATH", "./static/spec/private.json")
		options.SetDefault("PSKS", strings.Split(os.Getenv("EXPORTS_PSKS"), ","))
		options.SetDefault("EXPORT_EXPIRY_DAYS", 7)

		// DB defaults
		options.SetDefault("PGSQL_USER", "postgres")
		options.SetDefault("PGSQL_PASSWORD", "postgres")
		options.SetDefault("PGSQL_HOSTNAME", "localhost")
		options.SetDefault("PGSQL_PORT", "15433")
		options.SetDefault("PGSQL_DATABASE", "postgres")

		// Minio defaults
		options.SetDefault("MINIO_HOST", "localhost")
		options.SetDefault("MINIO_PORT", "9099")
		options.SetDefault("MINIO_SSL", false)

		// Kafka defaults
		options.SetDefault("KAFKA_ANNOUNCE_TOPIC", ExportTopic)
		options.SetDefault("KAFKA_BROKERS", strings.Split(os.Getenv("KAFKA_BROKERS"), ","))
		options.SetDefault("KAFKA_GROUP_ID", "export")
		options.SetDefault("KAFKA_EVENT_SOURCE", "urn:redhat:source:export-service")
		options.SetDefault("KAFKA_EVENT_SPECVERSION", "1.0")
		options.SetDefault("KAFKA_EVENT_TYPE", "com.redhat.console.export-service.request")
		options.SetDefault("KAFKA_EVENT_DATASCHEMA", "https://github.com/RedHatInsights/event-schemas/blob/main/schemas/apps/export-service/v1/export-request.json")

		options.AutomaticEnv()

		kubenv := viper.New()
		kubenv.AutomaticEnv()

		config = &ExportConfig{
			Hostname:           kubenv.GetString("Hostname"),
			PublicPort:         options.GetInt("PUBLIC_PORT"),
			MetricsPort:        options.GetInt("METRICS_PORT"),
			PrivatePort:        options.GetInt("PRIVATE_PORT"),
			Debug:              options.GetBool("DEBUG"),
			LogLevel:           options.GetString("LOG_LEVEL"),
			OpenAPIPublicPath:  options.GetString("OPEN_API_FILE_PATH"),
			OpenAPIPrivatePath: options.GetString("OPEN_API_PRIVATE_PATH"),
			Psks:               options.GetStringSlice("PSKS"),
			ExportExpiryDays:   options.GetInt("EXPORT_EXPIRY_DAYS"),
		}

		config.DBConfig = dbConfig{
			User:     options.GetString("PGSQL_USER"),
			Password: options.GetString("PGSQL_PASSWORD"),
			Hostname: options.GetString("PGSQL_HOSTNAME"),
			Port:     options.GetString("PGSQL_PORT"),
			Name:     options.GetString("PGSQL_DATABASE"),
			SSLCfg: dbSSLConfig{
				SSLMode: "disable",
			},
		}

		config.StorageConfig = storageConfig{
			Bucket:    "exports-bucket",
			Endpoint:  buildBaseHttpUrl(options.GetBool("MINIO_SSL"), options.GetString("MINIO_HOST"), options.GetInt("MINIO_PORT")),
			AccessKey: options.GetString("AWS_ACCESS_KEY"),
			SecretKey: options.GetString("AWS_SECRET_ACCESS_KEY"),
			UseSSL:    options.GetBool("MINIO_SSL"),
		}

		config.KafkaConfig = kafkaConfig{
			Brokers:          options.GetStringSlice("KAFKA_BROKERS"),
			GroupID:          options.GetString("KAFKA_GROUP_ID"),
			ExportsTopic:     options.GetString("KAFKA_ANNOUNCE_TOPIC"),
			EventSource:      options.GetString("KAFKA_EVENT_SOURCE"),
			EventSpecVersion: options.GetString("KAFKA_EVENT_SPECVERSION"),
			EventType:        options.GetString("KAFKA_EVENT_TYPE"),
			EventDataSchema:  options.GetString("KAFKA_EVENT_DATASCHEMA"),
		}

		if clowder.IsClowderEnabled() {
			cfg := clowder.LoadedConfig

			config.PublicPort = *cfg.PublicPort
			config.MetricsPort = cfg.MetricsPort
			config.PrivatePort = *cfg.PrivatePort

			exportBucket := options.GetString("EXPORT_SERVICE_BUCKET")
			exportBucketInfo := clowder.ObjectBuckets[exportBucket]

			rdsCaPath, err := getRdsCaPath(cfg)
			if err != nil {
				panic("RDS CA failed to write: " + err.Error())
			}

			config.DBConfig = dbConfig{
				User:     cfg.Database.Username,
				Password: cfg.Database.Password,
				Hostname: cfg.Database.Hostname,
				Port:     fmt.Sprint(cfg.Database.Port),
				Name:     cfg.Database.Name,
				SSLCfg: dbSSLConfig{
					SSLMode: cfg.Database.SslMode,
					RdsCa:   rdsCaPath,
				},
			}

			config.KafkaConfig.Brokers = clowder.KafkaServers
			broker := cfg.Kafka.Brokers[0]
			if broker.Authtype != nil {
				caPath, err := cfg.KafkaCa(broker)
				if err != nil {
					panic("Kafka CA failed to write")
				}
				securityProtocol := "sasl_ssl"
				if broker.SecurityProtocol != nil {
					securityProtocol = *broker.SecurityProtocol
				}
				config.KafkaConfig.SSLConfig = kafkaSSLConfig{
					Username:      *broker.Sasl.Username,
					Password:      *broker.Sasl.Password,
					SASLMechanism: *broker.Sasl.SaslMechanism,
					Protocol:      securityProtocol,
					CA:            caPath,
				}
			}

			config.Logging = &loggingConfig{
				AccessKeyID:     cfg.Logging.Cloudwatch.AccessKeyId,
				SecretAccessKey: cfg.Logging.Cloudwatch.SecretAccessKey,
				LogGroup:        cfg.Logging.Cloudwatch.LogGroup,
				Region:          cfg.Logging.Cloudwatch.Region,
			}

			bucket := cfg.ObjectStore.Buckets[0]
			config.StorageConfig = storageConfig{
				Bucket:    exportBucketInfo.RequestedName,
				Endpoint:  buildBaseHttpUrl(cfg.ObjectStore.Tls, cfg.ObjectStore.Hostname, cfg.ObjectStore.Port),
				AccessKey: *bucket.AccessKey,
				SecretKey: *bucket.SecretKey,
				UseSSL:    cfg.ObjectStore.Tls,
			}
		}
	})

	return config
}

func getRdsCaPath(cfg *clowder.AppConfig) (*string, error) {
	var rdsCaPath *string

	if cfg.Database.RdsCa != nil {
		rdsCaPathValue, err := cfg.RdsCa()
		if err != nil {
			return nil, err
		}
		rdsCaPath = &rdsCaPathValue
	}

	return rdsCaPath, nil
}

func buildBaseHttpUrl(tlsEnabled bool, hostname string, port int) string {
	var protocol string = "http"
	if tlsEnabled {
		protocol = "https"
	}

	return fmt.Sprintf("%s://%s:%d", protocol, hostname, port)
}
