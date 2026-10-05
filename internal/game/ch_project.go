package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"cloudquest/internal/cloud"
)

func projectChapter() *Chapter {
	return &Chapter{
		ID:          "projet",
		Title:       "Chapitre 4 — Projet final : le pipeline LivrExpress",
		Service:     "S3 + DynamoDB + SQS",
		LocalTool:   "MinIO + DynamoDB Local + ElasticMQ",
		Icon:        "🏗️",
		Color:       "#1d8102",
		RequiresAll: []string{"s3", "dynamodb", "sqs"},
		Intro: `<p>Vous maîtrisez les trois briques. Il est temps de les assembler en un vrai
<strong>pipeline événementiel</strong>, celui que LivrExpress mettra en production :</p>
<pre class="diagram">Site web ──► [SQS commandes-entrantes] ──► Worker Go ──► [DynamoDB Commandes]
                                               └──────► [S3 livrexpress-factures/recus/]</pre>
<p>Le squelette du worker se trouve dans <code>projet/pipeline/main.go</code>. Les connexions aux services
sont déjà écrites ; à vous de coder la logique métier marquée <code>TODO</code>.
Le cahier de projet PDF (<code>docs/</code>) détaille le contexte et les questions d'analyse.</p>`,
		Challenges: []*Challenge{projectPipeline(), projectResilience()},
		Quiz: []Question{
			{ID: "pq1", Text: "Le worker écrit dans DynamoDB, puis plante avant d'écrire le reçu S3. Le message sera relivré. Quelle propriété doit avoir le traitement ?",
				Options: []string{"Être rapide", "Être idempotent : le rejouer ne crée ni doublon ni incohérence", "Être transactionnel avec SQS", "Être synchrone"},
				Answer:  1, Explain: "Avec une livraison <em>at-least-once</em>, tout traitement peut être rejoué. Écrire avec la même clé (<code>commandeId</code>) et la même clé S3 rend les écritures idempotentes."},
			{ID: "pq2", Text: "Dans quel ordre le worker doit-il agir ?",
				Options: []string{"Supprimer le message, puis traiter", "Traiter (DynamoDB + S3), puis supprimer le message", "Peu importe", "Supprimer le message, puis traiter en arrière-plan"},
				Answer:  1, Explain: "On ne supprime qu'après succès complet. Supprimer d'abord, c'est risquer de perdre la commande si le traitement échoue."},
			{ID: "pq3", Text: "Pourquoi stocker le reçu dans S3 plutôt que dans l'item DynamoDB ?",
				Options: []string{"DynamoDB ne stocke pas de texte", "Un item DynamoDB est limité à 400 Ko et coûte plus cher au Go ; S3 est fait pour les documents", "S3 est plus rapide en lecture d'un champ", "Pour pouvoir faire des jointures"},
				Answer:  1, Explain: "Pattern classique : métadonnées et état dans DynamoDB, documents volumineux dans S3, avec la clé S3 référencée dans l'item si besoin."},
			{ID: "pq4", Text: "La charge triple pendant les soldes. Comment ce pipeline monte-t-il en charge ?",
				Options: []string{"Il faut un plus gros serveur de base de données", "On lance plus de workers : la file répartit les messages entre eux", "Impossible sans réécrire", "En augmentant le délai de visibilité"},
				Answer:  1, Explain: "La file découple producteurs et consommateurs : on ajoute des workers (scaling horizontal). DynamoDB et S3 absorbent la charge sans gestion de serveurs."},
			{ID: "pq5", Text: "En production sur AWS, comment le worker devrait-il obtenir ses identifiants ?",
				Options: []string{"Clés en dur dans le code", "Un fichier .env commité dans Git", "Un rôle IAM attaché (ECS task role, Lambda role…), avec le moindre privilège", "Les identifiants root du compte"},
				Answer:  2, Explain: "Jamais de clés en dur. Un rôle IAM fournit des identifiants temporaires renouvelés automatiquement, limités aux seules actions nécessaires."},
		},
	}
}

type order struct {
	CommandeID string  `json:"commandeId"`
	Client     string  `json:"client"`
	Montant    float64 `json:"montant"`
	Ville      string  `json:"ville"`
}

func randomOrders(n int) []order {
	out := make([]order, n)
	for i := range out {
		out[i] = order{
			CommandeID: fmt.Sprintf("CMD-2026-%05d", rand.IntN(99999)),
			Client:     clientsHisto[rand.IntN(len(clientsHisto))],
			Montant:    float64((10 + rand.IntN(900)) * 50),
			Ville:      villesHisto[rand.IntN(len(villesHisto))],
		}
	}
	return out
}

func checkPrereqs(ctx context.Context, c *cloud.Clients) (string, error) {
	u, err := queueURL(ctx, c, queueCommandes)
	if err != nil {
		return "", errors.New("la file commandes-entrantes est introuvable (chapitre 3)")
	}
	if _, err := describeTable(ctx, c, tableCommandes); err != nil {
		return "", errors.New("la table Commandes est introuvable (chapitre 2)")
	}
	if err := bucketExists(ctx, c, bucketFactures); err != nil {
		return "", errors.New("le bucket livrexpress-factures est introuvable (chapitre 1)")
	}
	return u, nil
}

func sendOrders(ctx context.Context, c *cloud.Clients, url string, orders []order) error {
	for _, o := range orders {
		b, _ := json.Marshal(o)
		if _, err := c.SQS.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(url), MessageBody: aws.String(string(b))}); err != nil {
			return err
		}
	}
	return nil
}

// verifyProcessed contrôle DynamoDB et S3 pour une liste de commandes attendues.
func verifyProcessed(ctx context.Context, c *cloud.Clients, r *report, orders []order) {
	okDB, okS3 := 0, 0
	var badDB, badS3 []string
	for _, o := range orders {
		out, err := c.Dynamo.GetItem(ctx, &dynamodb.GetItemInput{
			TableName: aws.String(tableCommandes),
			Key:       map[string]dtypes.AttributeValue{"commandeId": &dtypes.AttributeValueMemberS{Value: o.CommandeID}},
		})
		if err == nil && out.Item != nil {
			s, _ := attrS(out.Item, "statut")
			m, _ := attrN(out.Item, "montant")
			cl, _ := attrS(out.Item, "client")
			if s == "TRAITEE" && m == o.Montant && cl == o.Client {
				okDB++
			} else {
				badDB = append(badDB, o.CommandeID)
			}
		} else {
			badDB = append(badDB, o.CommandeID)
		}
		b, _, err := getObject(ctx, c, bucketFactures, "recus/"+o.CommandeID+".json")
		var doc map[string]any
		if err == nil && json.Unmarshal(b, &doc) == nil && doc["commandeId"] == o.CommandeID {
			okS3++
		} else {
			badS3 = append(badS3, o.CommandeID)
		}
	}
	r.ok(fmt.Sprintf("Commandes en base avec statut TRAITEE (%d/%d)", okDB, len(orders)), okDB == len(orders),
		"absentes ou incorrectes (statut, client, montant) : %s", strings.Join(first(badDB, 3), ", "))
	r.ok(fmt.Sprintf("Reçus JSON dans S3 recus/<commandeId>.json (%d/%d)", okS3, len(orders)), okS3 == len(orders),
		"reçus manquants ou sans commandeId : %s", strings.Join(first(badS3, 3), ", "))
}

func first(s []string, n int) []string {
	if len(s) > n {
		return append(s[:n:n], "…")
	}
	return s
}

func loadOrders(st *ChallengeState, key string) []order {
	var o []order
	_ = json.Unmarshal([]byte(st.Secret[key]), &o)
	return o
}

// --- Projet 1 --------------------------------------------------------------

func projectPipeline() *Challenge {
	return &Challenge{
		ID: "projet-pipeline", Title: "Mise en production du worker", XP: 400,
		Story: `<p>Jour J. Karim, Yasmine et Mehdi sont derrière vous. Le site va envoyer de vraies commandes
dans <code>commandes-entrantes</code>. Votre worker Go doit, pour <strong>chaque</strong> message :
l'enregistrer dans DynamoDB, déposer un reçu dans S3, puis supprimer le message.</p>`,
		Objective: `<ol><li>Compléter les <code>TODO</code> de <code>projet/pipeline/main.go</code>.</li>
<li>Cliquer sur « Envoyer les commandes du jour » (8 commandes aléatoires).</li>
<li>Lancer votre worker : <code>go run ./projet/pipeline</code>.</li>
<li>Vérifier. Pour chaque commande : item <code>Commandes</code> avec <code>client</code>, <code>montant</code> et <code>statut = TRAITEE</code> ;
objet <code>recus/&lt;commandeId&gt;.json</code> (JSON contenant <code>commandeId</code>) ; file vide.</li></ol>`,
		Concepts: []string{"Architecture événementielle", "SDK Go v2", "Idempotence", "Boucle de consommation"},
		Docs: []Link{
			{"SDK Go v2 — Guide", "https://aws.github.io/aws-sdk-go-v2/docs/"},
			{"attributevalue (marshal Go ↔ DynamoDB)", "https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"},
		},
		Setup: &SetupSpec{
			Label: "🚚 Envoyer les commandes du jour",
			Run: func(ctx context.Context, c *cloud.Clients, st *ChallengeState) (string, error) {
				u, err := checkPrereqs(ctx, c)
				if err != nil {
					return "", err
				}
				_, _ = c.SQS.PurgeQueue(ctx, &sqs.PurgeQueueInput{QueueUrl: aws.String(u)})
				orders := randomOrders(8)
				if err := sendOrders(ctx, c, u, orders); err != nil {
					return "", err
				}
				b, _ := json.Marshal(orders)
				st.Secret["orders"] = string(b)
				return "8 commandes publiées dans commandes-entrantes (la file a été vidée avant). Lancez votre worker !", nil
			},
		},
		Steps: []Step{
			{
				Title:   "Lire le squelette",
				Explain: `<p>Ouvrez <code>projet/pipeline/main.go</code>. Repérez ce qui est déjà fait (configuration, clients SDK) et les fonctions à écrire. Lisez les commentaires : ils décrivent le contrat de chaque fonction.</p>`,
				Hints: []string{
					"Le programme compile déjà : lancez-le tel quel, il vous dira ce qui manque.",
					"Travaillez une fonction à la fois et relancez souvent.",
				},
			},
			{
				Title:   "Recevoir les messages",
				Explain: `<p>Une boucle qui reçoit par paquets, traite chaque message, et s'arrête quand la file est vide (ou continue indéfiniment, comme un vrai worker).</p>`,
				Hints: []string{
					"<code>client.ReceiveMessage</code> prend un <code>*sqs.ReceiveMessageInput</code> ; souvenez-vous des limites vues au chapitre 3.",
					"Le corps est dans <code>msg.Body</code> (un <code>*string</code>) : <code>aws.ToString</code> le convertit, puis <code>json.Unmarshal</code> le décode dans la struct <code>Commande</code>.",
				},
			},
			{
				Title:   "Écrire dans DynamoDB",
				Explain: `<p>Convertir une struct Go en item DynamoDB à la main est fastidieux. Le paquet <code>attributevalue</code> sait le faire grâce aux tags de struct.</p>`,
				Hints: []string{
					"<code>attributevalue.MarshalMap(v)</code> renvoie une <code>map[string]types.AttributeValue</code>, directement utilisable dans <code>PutItemInput.Item</code>.",
					"Les tags <code>dynamodbav:\"…\"</code> de la struct fixent le nom des attributs. Pensez à positionner le statut avant d'écrire.",
				},
			},
			{
				Title:   "Écrire le reçu dans S3",
				Explain: `<p>Le reçu est un document JSON. Sa clé est déterministe (elle dépend uniquement du <code>commandeId</code>) : rejouer le message réécrit le même objet, sans doublon.</p>`,
				Hints: []string{
					"<code>PutObject</code> attend un <code>io.Reader</code> comme <code>Body</code> : <code>bytes.NewReader(b)</code> fait l'affaire.",
					"Pensez au <code>ContentType</code>, c'est une bonne habitude.",
				},
			},
			{
				Title:   "Acquitter",
				Explain: `<p>Le message n'est supprimé qu'une fois <strong>les deux</strong> écritures réussies.</p>`,
				Hints: []string{
					"<code>DeleteMessage</code> utilise le <code>ReceiptHandle</code> du message reçu.",
					"Si une écriture échoue, ne supprimez pas : loguez l'erreur et passez au message suivant. SQS relivrera.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, st *ChallengeState) []CheckItem {
			r := &report{}
			orders := loadOrders(st, "orders")
			if !r.ok("Commandes du jour envoyées", len(orders) > 0, "cliquez d'abord sur « Envoyer les commandes du jour »") {
				return r.pending("Commandes en base", "Reçus dans S3", "File commandes-entrantes vide")
			}
			verifyProcessed(ctx, c, r, orders)
			u, err := queueURL(ctx, c, queueCommandes)
			n := -1
			if err == nil {
				n, err = pendingCount(ctx, c, u)
			}
			r.ok("File commandes-entrantes vide", err == nil && n == 0, "%d message(s) restant(s) ou non acquittés", n)
			return r.items
		},
	}
}

// --- Projet 2 --------------------------------------------------------------

func projectResilience() *Challenge {
	return &Challenge{
		ID: "projet-resilience", Title: "Résister aux données toxiques", XP: 300,
		Story: `<p>Une semaine après la mise en prod, un partenaire envoie des commandes mal formées.
Le worker doit continuer à traiter les bonnes commandes, <strong>refuser</strong> les mauvaises,
et celles-ci doivent finir en quarantaine pour que l'équipe les analyse.</p>`,
		Objective: `<ol><li>Créer <code>commandes-entrantes-dlq</code> et relier <code>commandes-entrantes</code> à cette DLQ (<code>maxReceiveCount</code> ≤ 5).</li>
<li>Faire évoluer votre worker : une commande dont le corps n'est pas un JSON valide, ou dont le <code>montant</code> est ≤ 0, est <strong>rejetée</strong> (non écrite, non supprimée).</li>
<li>Cliquer sur « Envoyer un lot piégé », lancer le worker, attendre que les messages toxiques basculent en DLQ, puis vérifier.</li></ol>`,
		Concepts: []string{"Validation", "Gestion d'erreurs", "DLQ", "ChangeMessageVisibility"},
		Docs:     []Link{{"AWS — ChangeMessageVisibility", "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/APIReference/API_ChangeMessageVisibility.html"}},
		Setup: &SetupSpec{
			Label: "🧪 Envoyer un lot piégé",
			Run: func(ctx context.Context, c *cloud.Clients, st *ChallengeState) (string, error) {
				u, err := checkPrereqs(ctx, c)
				if err != nil {
					return "", err
				}
				a, _ := queueAttrs(ctx, c, u)
				var rp struct {
					Target string `json:"deadLetterTargetArn"`
					Max    any    `json:"maxReceiveCount"`
				}
				if json.Unmarshal([]byte(a["RedrivePolicy"]), &rp) != nil || rp.Target == "" {
					return "", errors.New("commandes-entrantes n'a pas de RedrivePolicy : sans DLQ, les messages toxiques tourneraient indéfiniment")
				}
				if max, _ := strconv.Atoi(fmt.Sprint(rp.Max)); max < 1 || max > 5 {
					return "", fmt.Errorf("maxReceiveCount vaut %v : choisissez une valeur entre 1 et 5", rp.Max)
				}
				dlq, err := queueURL(ctx, c, queueCommandesDLQ)
				if err != nil {
					return "", errors.New("la file commandes-entrantes-dlq est introuvable")
				}
				_, _ = c.SQS.PurgeQueue(ctx, &sqs.PurgeQueueInput{QueueUrl: aws.String(u)})
				_, _ = c.SQS.PurgeQueue(ctx, &sqs.PurgeQueueInput{QueueUrl: aws.String(dlq)})
				good := randomOrders(5)
				bad := randomOrders(2)
				bad[1].Montant = -1 * bad[1].Montant
				if err := sendOrders(ctx, c, u, good); err != nil {
					return "", err
				}
				// Message 1 : corps non JSON. Message 2 : montant négatif.
				raw := fmt.Sprintf("commandeId=%s;client=%s;montant=???", bad[0].CommandeID, bad[0].Client)
				if _, err := c.SQS.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(u), MessageBody: aws.String(raw)}); err != nil {
					return "", err
				}
				if err := sendOrders(ctx, c, u, bad[1:]); err != nil {
					return "", err
				}
				gb, _ := json.Marshal(good)
				bb, _ := json.Marshal(bad)
				st.Secret["good"], st.Secret["bad"] = string(gb), string(bb)
				return "7 messages envoyés (les files principale et DLQ ont été vidées avant). Certains sont piégés…", nil
			},
		},
		Steps: []Step{
			{
				Title:   "Brancher la DLQ",
				Explain: `<p>Vous l'avez déjà fait au chapitre 3 sur une autre file : même principe.</p>`,
				Hints:   []string{"La politique se pose sur la file source (<code>commandes-entrantes</code>), avec l'ARN de la DLQ."},
			},
			{
				Title:   "Valider avant d'écrire",
				Explain: `<p>Un worker robuste <strong>valide</strong> chaque message avant tout effet de bord. Une erreur de décodage ou une règle métier violée ne doit jamais faire planter le programme.</p>`,
				Hints: []string{
					"<code>json.Unmarshal</code> renvoie une erreur sur un corps invalide : ne la transformez pas en <code>log.Fatal</code> !",
					"Créez une fonction <code>valider(c Commande) error</code> qui vérifie les règles métier.",
				},
			},
			{
				Title:   "Accélérer la mise en quarantaine",
				Explain: `<p>Avec un délai de visibilité de 60 s et <code>maxReceiveCount</code> = 3, un message toxique met plusieurs minutes à atteindre la DLQ. On peut le rendre visible immédiatement en cas d'échec.</p>`,
				Hints: []string{
					"L'opération <code>ChangeMessageVisibility</code> modifie le délai d'un message déjà reçu.",
					"Un délai de 0 le rend aussitôt disponible pour une nouvelle tentative, ce qui incrémente plus vite son compteur de réceptions.",
					"Votre worker doit continuer à recevoir tant qu'il reste des messages, sinon les toxiques ne basculeront jamais.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, st *ChallengeState) []CheckItem {
			r := &report{}
			good, bad := loadOrders(st, "good"), loadOrders(st, "bad")
			if !r.ok("Lot piégé envoyé", len(good) > 0, "cliquez d'abord sur « Envoyer un lot piégé »") {
				return r.pending("Bonnes commandes traitées", "Commandes toxiques rejetées", "Toxiques en quarantaine (DLQ)", "File principale vide")
			}
			verifyProcessed(ctx, c, r, good)
			leaked := 0
			for _, o := range bad {
				out, err := c.Dynamo.GetItem(ctx, &dynamodb.GetItemInput{
					TableName: aws.String(tableCommandes),
					Key:       map[string]dtypes.AttributeValue{"commandeId": &dtypes.AttributeValueMemberS{Value: o.CommandeID}},
				})
				if err == nil && out.Item != nil {
					leaked++
				}
			}
			r.ok("Commandes toxiques rejetées (absentes de DynamoDB)", leaked == 0, "%d commande(s) toxique(s) ont été écrites en base", leaked)
			dlq, err := queueURL(ctx, c, queueCommandesDLQ)
			inDLQ := 0
			if err == nil {
				msgs, _ := peek(ctx, c, dlq)
				for _, o := range bad {
					for _, m := range msgs {
						if strings.Contains(aws.ToString(m.Body), o.CommandeID) {
							inDLQ++
							break
						}
					}
				}
			}
			r.ok(fmt.Sprintf("Toxiques en quarantaine dans la DLQ (%d/%d)", inDLQ, len(bad)), inDLQ == len(bad), "patientez ou accélérez avec ChangeMessageVisibility")
			u, _ := queueURL(ctx, c, queueCommandes)
			n, err := pendingCount(ctx, c, u)
			r.ok("File principale vide", err == nil && n == 0, "%d message(s) restant(s)", n)
			return r.items
		},
	}
}
