// Projet final CloudQuest — worker du pipeline de commandes LivrExpress.
//
//	Site web ──► [SQS commandes-entrantes] ──► CE PROGRAMME ──► [DynamoDB Commandes]
//	                                                └────────► [S3 livrexpress-factures/recus/]
//
// La configuration et les clients SDK sont fournis. À vous d'écrire les
// fonctions marquées TODO. Le programme compile et se lance tel quel :
//
//	go run ./projet/pipeline
//
// Il vous dira ce qui manque. Avancez une fonction à la fois.
package main

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	_ "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue" // à utiliser dans enregistrer() : retirez alors le « _ »
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	fileCommandes = "commandes-entrantes"
	tableCommande = "Commandes"
	bucketRecus   = "livrexpress-factures"
	prefixeRecus  = "recus/" // clé finale : recus/<commandeId>.json
)

// Commande est le contenu JSON d'un message de la file.
// Les tags json servent au décodage du message et au reçu S3 ;
// les tags dynamodbav fixent le nom des attributs dans la table.
type Commande struct {
	CommandeID string  `json:"commandeId" dynamodbav:"commandeId"`
	Client     string  `json:"client" dynamodbav:"client"`
	Montant    float64 `json:"montant" dynamodbav:"montant"`
	Ville      string  `json:"ville" dynamodbav:"ville"`
	Statut     string  `json:"statut" dynamodbav:"statut"`
}

// Worker regroupe les trois clients et l'URL de la file.
type Worker struct {
	sqs     *sqs.Client
	dynamo  *dynamodb.Client
	s3      *s3.Client
	fileURL string
}

var errAFaire = errors.New("TODO : fonction pas encore écrite")

// recevoir lit un paquet de messages dans la file.
//
// Contrat : renvoie les messages reçus (éventuellement aucun si la file est
// vide après une attente raisonnable). Ne supprime rien.
func (w *Worker) recevoir(ctx context.Context) ([]types.Message, error) {
	// TODO (étape « Recevoir les messages »)
	return nil, errAFaire
}

// decoder transforme le corps d'un message en Commande.
//
// Contrat : renvoie une erreur si le corps n'est pas un JSON valide.
func decoder(msg types.Message) (Commande, error) {
	// TODO (étape « Recevoir les messages »)
	return Commande{}, errAFaire
}

// valider applique les règles métier (défi « Résister aux données toxiques »).
//
// Contrat : renvoie nil si la commande est acceptable, une erreur sinon.
func valider(c Commande) error {
	// TODO (défi 2) — pour le défi 1, tout accepter suffit.
	return nil
}

// enregistrer écrit la commande dans la table DynamoDB, statut TRAITEE.
//
// Contrat : idempotent — rejouer la même commande ne crée pas de doublon.
func (w *Worker) enregistrer(ctx context.Context, c Commande) error {
	// TODO (étape « Écrire dans DynamoDB »)
	return errAFaire
}

// deposerRecu écrit le reçu JSON de la commande dans S3.
//
// Contrat : la clé ne dépend que du commandeId ; le document JSON contient
// au moins le champ commandeId.
func (w *Worker) deposerRecu(ctx context.Context, c Commande) error {
	// TODO (étape « Écrire le reçu dans S3 »)
	return errAFaire
}

// acquitter supprime de la file un message entièrement traité.
func (w *Worker) acquitter(ctx context.Context, msg types.Message) error {
	// TODO (étape « Acquitter »)
	return errAFaire
}

// rejeter est appelé quand un message ne peut pas être traité.
//
// Contrat : ne supprime PAS le message (SQS le relivrera, puis le basculera
// en DLQ). Peut accélérer la relivraison (défi 2, étape 3).
func (w *Worker) rejeter(ctx context.Context, msg types.Message, cause error) {
	log.Printf("✘ message %s rejeté : %v", aws.ToString(msg.MessageId), cause)
	// TODO (défi 2) — facultatif pour le défi 1.
}

// traiter enchaîne les étapes pour UN message. L'ordre est déjà le bon :
// on n'acquitte qu'après le succès des deux écritures.
func (w *Worker) traiter(ctx context.Context, msg types.Message) error {
	c, err := decoder(msg)
	if err != nil {
		return err
	}
	if err := valider(c); err != nil {
		return err
	}
	if err := w.enregistrer(ctx, c); err != nil {
		return err
	}
	if err := w.deposerRecu(ctx, c); err != nil {
		return err
	}
	return w.acquitter(ctx, msg)
}

func main() {
	ctx := context.Background()
	w, err := nouveauWorker(ctx)
	if err != nil {
		log.Fatalf("démarrage impossible : %v", err)
	}
	log.Printf("worker démarré, file : %s", w.fileURL)

	videsConsecutifs := 0
	for videsConsecutifs < 3 { // on s'arrête après 3 réceptions vides d'affilée
		msgs, err := w.recevoir(ctx)
		if errors.Is(err, errAFaire) {
			log.Fatal("recevoir() n'est pas encore écrite — commencez par là.")
		}
		if err != nil {
			log.Fatalf("réception : %v", err)
		}
		if len(msgs) == 0 {
			videsConsecutifs++
			continue
		}
		videsConsecutifs = 0
		for _, m := range msgs {
			if err := w.traiter(ctx, m); err != nil {
				w.rejeter(ctx, m, err)
				continue
			}
			log.Printf("✔ message %s traité", aws.ToString(m.MessageId))
		}
	}
	log.Print("file vide, arrêt du worker.")
}

// --- Fourni : configuration et clients (ne pas modifier) -------------------

func nouveauWorker(ctx context.Context) (*Worker, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(env("CQ_REGION", "us-east-1")),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			env("CQ_ACCESS_KEY", "cloudquest"), env("CQ_SECRET_KEY", "cloudquest-secret"), "")),
	)
	if err != nil {
		return nil, err
	}
	w := &Worker{
		sqs: sqs.NewFromConfig(cfg, func(o *sqs.Options) {
			o.BaseEndpoint = aws.String(env("CQ_SQS_ENDPOINT", "http://127.0.0.1:9324"))
		}),
		dynamo: dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
			o.BaseEndpoint = aws.String(env("CQ_DYNAMO_ENDPOINT", "http://127.0.0.1:8100"))
		}),
		s3: s3.NewFromConfig(cfg, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(env("CQ_S3_ENDPOINT", "http://127.0.0.1:9100"))
			o.UsePathStyle = true // indispensable avec MinIO
		}),
	}
	out, err := w.sqs.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(fileCommandes)})
	if err != nil {
		return nil, errors.New("file " + fileCommandes + " introuvable — avez-vous terminé le chapitre 3 ?")
	}
	w.fileURL = aws.ToString(out.QueueUrl)
	return w, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
