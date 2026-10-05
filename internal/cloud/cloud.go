// Package cloud construit les clients AWS SDK v2 pointés vers les
// émulateurs locaux (MinIO pour S3, DynamoDB Local, ElasticMQ pour SQS).
package cloud

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
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

	// Mode plateforme partagée (une instance pour toute une classe).
	// Les adresses publiques sont celles que les étudiants utilisent depuis
	// leur poste ; le jeu, lui, garde les adresses internes ci-dessus.
	Hosted        bool
	S3Public      string
	DynamoPublic  string
	SQSPublic     string
	StudentKey    string // identifiants communiqués aux étudiants
	StudentSecret string
}

// ConfigFromEnv lit la configuration depuis l'environnement, avec des
// valeurs par défaut alignées sur docker-compose.yml.
func ConfigFromEnv() Config {
	c := Config{
		S3Endpoint:     env("CQ_S3_ENDPOINT", "http://127.0.0.1:9100"),
		S3Console:      optional(env("CQ_S3_CONSOLE", "http://127.0.0.1:9101")),
		DynamoEndpoint: env("CQ_DYNAMO_ENDPOINT", "http://127.0.0.1:8100"),
		SQSEndpoint:    env("CQ_SQS_ENDPOINT", "http://127.0.0.1:9324"),
		SQSConsole:     optional(env("CQ_SQS_CONSOLE", "http://127.0.0.1:9325")),
		AccessKey:      env("CQ_ACCESS_KEY", "cloudquest"),
		SecretKey:      env("CQ_SECRET_KEY", "cloudquest-secret"),
		Region:         env("CQ_REGION", "us-east-1"),
		Hosted:         os.Getenv("CQ_MULTI") == "1",
	}
	c.S3Public = strings.TrimRight(env("CQ_S3_PUBLIC", c.S3Endpoint), "/")
	c.DynamoPublic = strings.TrimRight(env("CQ_DYNAMO_PUBLIC", c.DynamoEndpoint), "/")
	c.SQSPublic = strings.TrimRight(env("CQ_SQS_PUBLIC", c.SQSEndpoint), "/")
	c.StudentKey = env("CQ_STUDENT_ACCESS_KEY", c.AccessKey)
	c.StudentSecret = env("CQ_STUDENT_SECRET_KEY", c.SecretKey)
	return c
}

// optional permet de masquer une interface web non exposée (valeur « none »).
func optional(v string) string {
	if v == "none" {
		return ""
	}
	return v
}

// Display renvoie la configuration telle qu'on la montre aux étudiants :
// adresses publiques et identifiants étudiants.
func (c Config) Display() Config {
	c.S3Endpoint, c.DynamoEndpoint, c.SQSEndpoint = c.S3Public, c.DynamoPublic, c.SQSPublic
	c.AccessKey, c.SecretKey = c.StudentKey, c.StudentSecret
	return c
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
	// Suffix distingue les ressources d'un·e étudiant·e sur une plateforme
	// partagée (vide en local : les noms du jeu sont utilisés tels quels).
	Suffix string
}

// For renvoie une vue des clients dédiée à un suffixe d'étudiant·e.
func (c *Clients) For(suffix string) *Clients {
	cp := *c
	cp.Suffix = suffix
	return &cp
}

// N renvoie le nom réel d'une ressource (bucket, table, file) pour
// l'étudiant·e courant·e : « commandes-entrantes » devient
// « commandes-entrantes-amina », « paiements.fifo » « paiements-amina.fifo ».
func (c *Clients) N(name string) string { return Suffixed(name, c.Suffix) }

// Suffixed applique un suffixe d'étudiant·e à un nom de ressource.
func Suffixed(name, suffix string) string {
	if suffix == "" {
		return name
	}
	if base, ok := strings.CutSuffix(name, ".fifo"); ok {
		return base + "-" + suffix + ".fifo"
	}
	return name + "-" + suffix
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
