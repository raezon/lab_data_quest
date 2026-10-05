// Package cloud construit les clients AWS SDK v2 pointés vers les
// émulateurs locaux (MinIO pour S3, DynamoDB Local, ElasticMQ pour SQS).
package cloud

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// Config regroupe les points d'accès et identifiants des émulateurs.
type Config struct {
	S3Endpoint     string
	S3Console      string
	DynamoEndpoint string
	SQSEndpoint    string
	SQSConsole     string
	AccessKey      string
	SecretKey      string
	Region         string
}

// ConfigFromEnv lit la configuration depuis l'environnement, avec des
// valeurs par défaut alignées sur docker-compose.yml.
func ConfigFromEnv() Config {
	return Config{
		S3Endpoint:     env("CQ_S3_ENDPOINT", "http://127.0.0.1:9100"),
		S3Console:      env("CQ_S3_CONSOLE", "http://127.0.0.1:9101"),
		DynamoEndpoint: env("CQ_DYNAMO_ENDPOINT", "http://127.0.0.1:8100"),
		SQSEndpoint:    env("CQ_SQS_ENDPOINT", "http://127.0.0.1:9324"),
		SQSConsole:     env("CQ_SQS_CONSOLE", "http://127.0.0.1:9325"),
		AccessKey:      env("CQ_ACCESS_KEY", "cloudquest"),
		SecretKey:      env("CQ_SECRET_KEY", "cloudquest-secret"),
		Region:         env("CQ_REGION", "us-east-1"),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Clients contient un client par service.
type Clients struct {
	Cfg    Config
	S3     *s3.Client
	Dynamo *dynamodb.Client
	SQS    *sqs.Client
	HTTP   *http.Client
}

// New crée les clients SDK.
func New(ctx context.Context, c Config) (*Clients, error) {
	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(c.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, "")),
	)
	if err != nil {
		return nil, err
	}
	return &Clients{
		Cfg: c,
		S3: s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(c.S3Endpoint)
			o.UsePathStyle = true
		}),
		Dynamo: dynamodb.NewFromConfig(awsCfg, func(o *dynamodb.Options) {
			o.BaseEndpoint = aws.String(c.DynamoEndpoint)
		}),
		SQS: sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
			o.BaseEndpoint = aws.String(c.SQSEndpoint)
		}),
		HTTP: &http.Client{Timeout: 5 * time.Second},
	}, nil
}

// Health indique si chaque émulateur répond sur son port TCP.
func (c *Clients) Health() map[string]bool {
	return map[string]bool{
		"s3":       reachable(c.Cfg.S3Endpoint),
		"dynamodb": reachable(c.Cfg.DynamoEndpoint),
		"sqs":      reachable(c.Cfg.SQSEndpoint),
	}
}

func reachable(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	conn, err := net.DialTimeout("tcp", u.Host, 700*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
