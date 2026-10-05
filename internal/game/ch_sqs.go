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
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"cloudquest/internal/cloud"
)

const (
	queueCommandes    = "commandes-entrantes"
	queueCommandesDLQ = "commandes-entrantes-dlq"
	queueMystere      = "colis-mystere"
	queuePaiement     = "commandes-paiement"
	queuePaiementDLQ  = "commandes-paiement-dlq"
	queueFifo         = "paiements.fifo"
)

func sqsChapter() *Chapter {
	return &Chapter{
		ID:        "sqs",
		Title:     "Chapitre 3 — Le tapis roulant",
		Service:   "Amazon SQS",
		LocalTool: "ElasticMQ",
		Icon:      "📨",
		Color:     "#e7157b",
		Intro: `<p>Les jours de promotion, le site reçoit 50 commandes par seconde et le système de facturation
s'écroule. Solution classique : placer une <strong>file de messages</strong> entre les deux. Le site dépose,
la facturation consomme à son rythme. Sur AWS c'est <strong>SQS</strong> ; ici, <strong>ElasticMQ</strong>
implémente la même API.</p>
<p>Notions clés : file standard et FIFO, délai de visibilité, suppression explicite, file de lettres mortes (DLQ), déduplication.</p>`,
		Challenges: []*Challenge{sqsCreateQueue(), sqsSend(), sqsMystery(), sqsDLQ(), sqsFIFO()},
		Quiz: []Question{
			{ID: "qq1", Text: "Un consommateur reçoit un message puis plante avant de le supprimer. Que devient le message ?",
				Options: []string{"Il est perdu", "Il redevient visible après le délai de visibilité et sera relivré", "Il part immédiatement en DLQ", "Il est dupliqué dans une autre file"},
				Answer:  1, Explain: "Recevoir un message ne le supprime pas : il est seulement masqué pendant le <em>visibility timeout</em>. Sans <code>DeleteMessage</code>, il réapparaît. C'est ce qui rend SQS fiable."},
			{ID: "qq2", Text: "Une file SQS standard garantit…",
				Options: []string{"Une livraison exactement une fois, dans l'ordre", "Une livraison au moins une fois, ordre au mieux", "Aucune garantie", "L'ordre strict mais des pertes possibles"},
				Answer:  1, Explain: "File standard : <em>at-least-once</em>, ordre « best effort ». Votre traitement doit donc être <strong>idempotent</strong>. La FIFO apporte ordre strict et déduplication."},
			{ID: "qq3", Text: "À quoi sert une Dead-Letter Queue (DLQ) ?",
				Options: []string{"À stocker les messages supprimés", "À isoler les messages qui échouent trop souvent pour les analyser", "À accélérer la file principale", "À chiffrer les messages"},
				Answer:  1, Explain: "Après <code>maxReceiveCount</code> réceptions sans suppression, le message est déplacé en DLQ : il ne bloque plus le flux et reste disponible pour diagnostic ou rejeu."},
			{ID: "qq4", Text: "Combien de messages peut-on recevoir au maximum en un seul appel <code>ReceiveMessage</code> ?",
				Options: []string{"1", "10", "100", "Illimité"},
				Answer:  1, Explain: "10 messages maximum par appel. Pour vider une file il faut donc boucler."},
			{ID: "qq5", Text: "Dans une file FIFO, le <code>MessageGroupId</code> sert à…",
				Options: []string{"Choisir la région", "Définir l'unité d'ordonnancement : l'ordre est garanti au sein d'un même groupe", "Chiffrer le message", "Fixer la priorité"},
				Answer:  1, Explain: "L'ordre est strict <strong>par groupe</strong>. Des groupes différents peuvent être traités en parallèle par plusieurs consommateurs."},
			{ID: "qq6", Text: "Qu'apporte le <em>long polling</em> (<code>WaitTimeSeconds</code> > 0) ?",
				Options: []string{"Des messages plus gros", "Moins de réponses vides et donc moins d'appels facturés", "Un ordre garanti", "Une rétention plus longue"},
				Answer:  1, Explain: "Le serveur attend jusqu'à 20 s qu'un message arrive avant de répondre. Moins d'appels vides = moins de coût et moins de CPU."},
		},
	}
}

// --- outils communs --------------------------------------------------------

func queueURL(ctx context.Context, c *cloud.Clients, name string) (string, error) {
	out, err := c.SQS.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.QueueUrl), nil
}

func queueAttrs(ctx context.Context, c *cloud.Clients, url string) (map[string]string, error) {
	out, err := c.SQS.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(url), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameAll},
	})
	if err != nil {
		return nil, err
	}
	return out.Attributes, nil
}

// pendingCount renvoie le nombre de messages visibles + en cours de traitement.
func pendingCount(ctx context.Context, c *cloud.Clients, url string) (int, error) {
	a, err := queueAttrs(ctx, c, url)
	if err != nil {
		return 0, err
	}
	v, _ := strconv.Atoi(a["ApproximateNumberOfMessages"])
	nv, _ := strconv.Atoi(a["ApproximateNumberOfMessagesNotVisible"])
	return v + nv, nil
}

// peek lit les messages sans les consommer (délai de visibilité nul).
func peek(ctx context.Context, c *cloud.Clients, url string) ([]types.Message, error) {
	seen := map[string]bool{}
	var msgs []types.Message
	for range 4 {
		out, err := c.SQS.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(url), MaxNumberOfMessages: 10, VisibilityTimeout: 0,
			MessageAttributeNames:       []string{"All"},
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
		})
		if err != nil {
			return nil, err
		}
		added := 0
		for _, m := range out.Messages {
			if !seen[aws.ToString(m.MessageId)] {
				seen[aws.ToString(m.MessageId)] = true
				msgs = append(msgs, m)
				added++
			}
		}
		if added == 0 {
			break
		}
	}
	return msgs, nil
}

func ensureQueue(ctx context.Context, c *cloud.Clients, name string) (string, error) {
	out, err := c.SQS.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(name)})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.QueueUrl), nil
}

var stepConnectSQS = Step{
	Title: "Parler à ElasticMQ",
	Explain: `<p>ElasticMQ implémente l'API SQS : vous utilisez donc <code>aws sqs …</code> exactement comme sur AWS.
Une interface web en lecture seule permet de voir l'état des files.</p>`,
	Hints: []string{
		"L'API SQS écoute sur le port <strong>9324</strong> ; l'interface web sur <strong>9325</strong>.",
		"Les commandes SQS manipulent surtout des <strong>URL de file</strong>, pas des noms : <code>get-queue-url</code> fait la conversion.",
	},
}

// --- Défi 1 ----------------------------------------------------------------

func sqsCreateQueue() *Challenge {
	return &Challenge{
		ID: "sqs-queue", Title: "Installer le tapis roulant", XP: 100,
		Story: `<p>Mehdi, développeur back-end : « Le traitement d'une commande prend jusqu'à 40 secondes
(paiement, stock, facture). Il ne faudrait pas qu'un autre worker la récupère pendant ce temps. »</p>`,
		Objective: `Créer la file standard <code>commandes-entrantes</code> avec un <strong>délai de visibilité de 60 secondes</strong>.`,
		Concepts:  []string{"File standard", "Visibility timeout", "Attributs de file"},
		Docs:      []Link{{"AWS — Délai de visibilité", "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-visibility-timeout.html"}},
		Steps: []Step{
			stepConnectSQS,
			{
				Title:   "Créer la file avec le bon réglage",
				Explain: `<p>Le délai de visibilité doit être <strong>supérieur</strong> au temps de traitement maximal, sinon un message pourrait être traité deux fois en parallèle.</p>`,
				Hints: []string{
					"<code>aws sqs create-queue</code> accepte <code>--attributes</code> sous la forme <code>Nom=Valeur</code>.",
					"Le nom exact de l'attribut est <code>VisibilityTimeout</code>, en secondes.",
					"Si la file existe déjà, <code>set-queue-attributes</code> permet de la modifier ; <code>get-queue-attributes --attribute-names All</code> permet de vérifier.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			u, err := queueURL(ctx, c, queueCommandes)
			if !r.ok("La file commandes-entrantes existe", err == nil, errDetail(err)) {
				return r.pending("Délai de visibilité = 60 s")
			}
			a, err := queueAttrs(ctx, c, u)
			r.ok("Délai de visibilité = 60 s", err == nil && a["VisibilityTimeout"] == "60", "valeur actuelle : %s s", a["VisibilityTimeout"])
			return r.items
		},
	}
}

// --- Défi 2 ----------------------------------------------------------------

func sqsSend() *Challenge {
	return &Challenge{
		ID: "sqs-send", Title: "Premier colis sur le tapis", XP: 120,
		Story: `<p>Le site doit publier chaque nouvelle commande dans la file. Mehdi veut aussi savoir
d'où vient chaque message (site, appli mobile, centre d'appels) <strong>sans</strong> devoir
décoder le corps.</p>`,
		Objective: `Envoyer dans <code>commandes-entrantes</code> au moins un message dont :
<ul><li>le corps est un JSON contenant un champ <code>commandeId</code> ;</li>
<li>l'<strong>attribut de message</strong> <code>source</code> (type String) vaut <code>site-web</code>.</li></ul>
<p><em>Ne consommez pas votre message : laissez-le dans la file pour la vérification.</em></p>`,
		Concepts: []string{"SendMessage", "Corps de message", "Message attributes"},
		Docs:     []Link{{"AWS — Attributs de message", "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-message-metadata.html"}},
		Steps: []Step{
			{
				Title:   "Corps vs attributs",
				Explain: `<p>Le <strong>corps</strong> transporte la donnée métier (jusqu'à 256 Ko). Les <strong>attributs</strong> sont des métadonnées typées, lisibles sans parser le corps (utile pour le routage et le filtrage).</p>`,
				Hints: []string{
					"<code>aws sqs send-message</code> prend <code>--queue-url</code> et <code>--message-body</code>.",
					"Pour les attributs : <code>--message-attributes</code>. Chaque attribut a un <code>DataType</code> et une <code>StringValue</code>.",
					"Les guillemets imbriqués sont pénibles en shell : écrivez les attributs dans un fichier et utilisez <code>file://attributs.json</code>.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			u, err := queueURL(ctx, c, queueCommandes)
			if !r.ok("La file commandes-entrantes existe", err == nil, errDetail(err)) {
				return r.pending("Message JSON avec commandeId", "Attribut source = site-web")
			}
			msgs, err := peek(ctx, c, u)
			if !r.ok("La file contient des messages", err == nil && len(msgs) > 0, "file vide — avez-vous consommé votre message ? %s", errDetail(err)) {
				return r.pending("Message JSON avec commandeId", "Attribut source = site-web")
			}
			okBody, okAttr := false, false
			for _, m := range msgs {
				var doc map[string]any
				hasID := json.Unmarshal([]byte(aws.ToString(m.Body)), &doc) == nil && doc["commandeId"] != nil
				if hasID {
					okBody = true
					if a, ok := m.MessageAttributes["source"]; ok && aws.ToString(a.StringValue) == "site-web" {
						okAttr = true
					}
				}
			}
			r.ok("Message JSON avec commandeId", okBody, "aucun message n'a un corps JSON avec commandeId")
			r.ok("Attribut source = site-web", okAttr, "le message JSON n'a pas d'attribut de message source=site-web (le corps ne compte pas)")
			return r.items
		},
	}
}

// --- Défi 3 ----------------------------------------------------------------

func sqsMystery() *Challenge {
	return &Challenge{
		ID: "sqs-mystere", Title: "Le colis mystère", XP: 200,
		Story: `<p>Un colis contenant le code d'accès au nouvel entrepôt de Sétif a été découpé en morceaux,
mélangés parmi des messages ordinaires dans la file <code>colis-mystere</code>. Récupérez tous les fragments,
remettez-les dans l'ordre… et ne laissez aucun message traîner derrière vous.</p>`,
		Objective: `Cliquez sur « Charger les colis ». Consommez <strong>tous</strong> les messages de <code>colis-mystere</code>,
reconstituez le code (fragments triés par leur champ <code>ordre</code>) et saisissez-le.
La file doit être <strong>vide</strong> (messages supprimés) au moment de la vérification.`,
		Concepts: []string{"ReceiveMessage", "Limite de 10", "DeleteMessage", "ReceiptHandle"},
		Docs:     []Link{{"AWS — Recevoir et supprimer", "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/step-receive-delete-message.html"}},
		Input:    &InputSpec{Label: "Code reconstitué", Placeholder: "ex. AB12CD34"},
		Setup: &SetupSpec{
			Label: "📦 Charger les colis",
			Run:   setupMystere,
		},
		Steps: []Step{
			{
				Title:   "Recevoir tous les messages",
				Explain: `<p>Une réception renvoie <strong>au maximum 10</strong> messages, et parfois moins. Il faut donc recommencer jusqu'à ce que la file soit vide.</p>`,
				Hints: []string{
					"Option utile de <code>receive-message</code> : <code>--max-number-of-messages</code>.",
					"Une réception peut revenir vide alors que la file ne l'est pas : un peu de <em>long polling</em> (<code>--wait-time-seconds</code>) aide.",
				},
			},
			{
				Title:   "Supprimer ce qu'on a traité",
				Explain: `<p>Recevoir ≠ supprimer. Après traitement, on supprime chaque message avec son <strong>ReceiptHandle</strong> (pas son MessageId !).</p>`,
				Hints: []string{
					"<code>aws sqs delete-message</code> attend <code>--receipt-handle</code>.",
					"Un ReceiptHandle n'est valable que pour la réception qui l'a produit : supprimez vite, avant la fin du délai de visibilité.",
					"Écrire une petite boucle (bash + jq, ou Go) est bien plus confortable que de copier-coller : c'est l'occasion de vous y mettre.",
				},
			},
			{
				Title:   "Reconstituer le code",
				Explain: `<p>L'ordre d'arrivée d'une file standard n'est pas garanti : ne vous y fiez pas, utilisez le champ <code>ordre</code>.</p>`,
				Hints: []string{
					"Seuls certains messages ont un champ <code>fragment</code> ; les autres sont des leurres.",
					"Vous avez perdu des fragments en supprimant trop vite ? Rechargez simplement les colis : un nouveau code sera généré.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, answer string, st *ChallengeState) []CheckItem {
			r := &report{}
			want, ok := st.Secret["code"]
			if !r.ok("Colis chargés", ok, "cliquez d'abord sur « Charger les colis »") {
				return r.pending("Code correct", "File colis-mystere vide")
			}
			norm := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(answer))
			r.ok("Code correct", norm == want, "ce n'est pas le bon code : avez-vous tous les fragments, dans l'ordre ?")
			u, err := queueURL(ctx, c, queueMystere)
			n := -1
			if err == nil {
				n, err = pendingCount(ctx, c, u)
			}
			r.ok("File colis-mystere vide", err == nil && n == 0, "%d message(s) encore présents ou en cours de traitement — supprimez-les", n)
			return r.items
		},
	}
}

func setupMystere(ctx context.Context, c *cloud.Clients, st *ChallengeState) (string, error) {
	u, err := ensureQueue(ctx, c, queueMystere)
	if err != nil {
		return "", err
	}
	_, _ = c.SQS.PurgeQueue(ctx, &sqs.PurgeQueueInput{QueueUrl: aws.String(u)})
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	nFrag := 4
	frags := make([]string, nFrag)
	for i := range frags {
		frags[i] = string([]byte{alphabet[rand.IntN(len(alphabet))], alphabet[rand.IntN(len(alphabet))]})
	}
	var bodies []string
	for i, f := range frags {
		b, _ := json.Marshal(map[string]any{"colis": fmt.Sprintf("COL-%04d", rand.IntN(9999)), "contenu": "pièce scellée",
			"fragment": map[string]any{"ordre": i + 1, "valeur": f}})
		bodies = append(bodies, string(b))
	}
	contents := []string{"cartons vides", "rouleaux de scotch", "gilets fluo", "étiquettes", "palettes", "terminal de paiement", "casques", "sacs isothermes"}
	for range 14 {
		b, _ := json.Marshal(map[string]any{"colis": fmt.Sprintf("COL-%04d", rand.IntN(9999)), "contenu": contents[rand.IntN(len(contents))]})
		bodies = append(bodies, string(b))
	}
	rand.Shuffle(len(bodies), func(i, j int) { bodies[i], bodies[j] = bodies[j], bodies[i] })
	for _, b := range bodies {
		if _, err := c.SQS.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(u), MessageBody: aws.String(b)}); err != nil {
			return "", err
		}
	}
	st.Secret["code"] = strings.Join(frags, "")
	return fmt.Sprintf("%d colis déposés dans la file %s. Les fragments sont quelque part là-dedans…", len(bodies), queueMystere), nil
}

// --- Défi 4 ----------------------------------------------------------------

func sqsDLQ() *Challenge {
	return &Challenge{
		ID: "sqs-dlq", Title: "Le message empoisonné", XP: 200,
		Story: `<p>Vendredi soir, alerte : le service de paiement redémarre en boucle. Une commande
corrompue (montant illisible) fait planter le worker, le message réapparaît, le worker replante…
Il faut un mécanisme pour mettre ce genre de message <strong>en quarantaine</strong> automatiquement.</p>`,
		Objective: `<ol><li>Créer la file <code>commandes-paiement-dlq</code>.</li>
<li>Créer (ou modifier) la file <code>commandes-paiement</code> avec une <strong>politique de redirection</strong> vers cette DLQ et <code>maxReceiveCount = 3</code>.</li>
<li>Cliquer sur « Injecter le message empoisonné ».</li>
<li>Jouer le worker défaillant : recevoir ce message plusieurs fois <strong>sans le supprimer</strong> jusqu'à ce qu'il bascule en DLQ.</li></ol>`,
		Concepts: []string{"Dead-letter queue", "RedrivePolicy", "maxReceiveCount", "ApproximateReceiveCount"},
		Docs:     []Link{{"AWS — Files de lettres mortes", "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/sqs-dead-letter-queues.html"}},
		Setup: &SetupSpec{
			Label: "☠️ Injecter le message empoisonné",
			Run: func(ctx context.Context, c *cloud.Clients, st *ChallengeState) (string, error) {
				u, err := queueURL(ctx, c, queuePaiement)
				if err != nil {
					return "", errors.New("la file commandes-paiement n'existe pas encore")
				}
				a, _ := queueAttrs(ctx, c, u)
				if a["RedrivePolicy"] == "" {
					return "", errors.New("commandes-paiement n'a pas de RedrivePolicy : le message tournerait en boucle à l'infini ! Configurez d'abord la DLQ")
				}
				id := fmt.Sprintf("CMD-POISON-%04d", rand.IntN(9999))
				body := fmt.Sprintf(`{"commandeId":%q,"montant":"#VALEUR!","client":"???"}`, id)
				if _, err := c.SQS.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(u), MessageBody: aws.String(body)}); err != nil {
					return "", err
				}
				st.Secret["poison"] = id
				return "Message " + id + " injecté dans commandes-paiement. À vous de le faire échouer… jusqu'à la quarantaine.", nil
			},
		},
		Steps: []Step{
			{
				Title:   "Créer la DLQ et la relier",
				Explain: `<p>La DLQ est une file ordinaire. C'est la file <strong>source</strong> qui porte l'attribut <code>RedrivePolicy</code> : un JSON contenant l'<strong>ARN</strong> de la DLQ et le nombre maximal de réceptions.</p>`,
				Hints: []string{
					"L'ARN d'une file se lit dans ses attributs (<code>QueueArn</code>).",
					"La valeur de <code>RedrivePolicy</code> est elle-même une <em>chaîne</em> contenant du JSON : attention à l'échappement des guillemets. Un fichier d'attributs passé en <code>file://</code> vous simplifiera la vie.",
					"Les deux clés de la politique s'appellent <code>deadLetterTargetArn</code> et <code>maxReceiveCount</code>.",
				},
			},
			{
				Title:   "Faire échouer le message",
				Explain: `<p>Chaque réception incrémente le compteur <code>ApproximateReceiveCount</code>. Quand il dépasse <code>maxReceiveCount</code>, le message est déplacé vers la DLQ à la tentative suivante.</p>`,
				Hints: []string{
					"Ne supprimez surtout pas le message : c'est justement le scénario d'un worker qui plante.",
					"Le message reste invisible pendant le délai de visibilité après chaque réception. Pour ne pas attendre, regardez l'option <code>--visibility-timeout</code> de <code>receive-message</code>.",
					"Ajoutez <code>--attribute-names ApproximateReceiveCount</code> pour suivre le compteur. Quand la file principale semble vide, regardez dans la DLQ.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, st *ChallengeState) []CheckItem {
			r := &report{}
			dlqURL, err := queueURL(ctx, c, queuePaiementDLQ)
			r.ok("La file commandes-paiement-dlq existe", err == nil, errDetail(err))
			srcURL, err2 := queueURL(ctx, c, queuePaiement)
			if !r.ok("La file commandes-paiement existe", err2 == nil, errDetail(err2)) || err != nil {
				return r.pending("RedrivePolicy vers la DLQ", "maxReceiveCount = 3", "Message empoisonné en quarantaine")
			}
			a, _ := queueAttrs(ctx, c, srcURL)
			da, _ := queueAttrs(ctx, c, dlqURL)
			var rp struct {
				Target string `json:"deadLetterTargetArn"`
				Max    any    `json:"maxReceiveCount"`
			}
			_ = json.Unmarshal([]byte(a["RedrivePolicy"]), &rp)
			r.ok("RedrivePolicy vers la DLQ", rp.Target != "" && rp.Target == da["QueueArn"], "cible actuelle : %q (attendu : %s)", rp.Target, da["QueueArn"])
			r.ok("maxReceiveCount = 3", fmt.Sprint(rp.Max) == "3", "valeur actuelle : %v", rp.Max)
			id, ok := st.Secret["poison"]
			if !r.ok("Message empoisonné injecté", ok, "cliquez sur « Injecter le message empoisonné »") {
				return r.pending("Message empoisonné en quarantaine")
			}
			msgs, _ := peek(ctx, c, dlqURL)
			found := false
			for _, m := range msgs {
				if strings.Contains(aws.ToString(m.Body), id) {
					found = true
				}
			}
			r.ok("Message empoisonné en quarantaine", found, "%s n'est pas (encore) dans la DLQ : recevez-le encore sans le supprimer", id)
			return r.items
		},
	}
}

// --- Défi 5 ----------------------------------------------------------------

func sqsFIFO() *Challenge {
	return &Challenge{
		ID: "sqs-fifo", Title: "Chaque chose en son temps", XP: 150,
		Story: `<p>Pour les paiements, l'ordre compte : « autorisation » puis « capture » puis « remboursement ».
Traiter un remboursement avant la capture serait une catastrophe comptable. Et un double clic
ne doit pas débiter deux fois le client.</p>`,
		Objective: `Créer la file FIFO <code>paiements.fifo</code> avec la <strong>déduplication basée sur le contenu</strong>.
Y envoyer, dans le groupe <code>client-42</code>, trois messages JSON <code>{"etape":1}</code>, <code>{"etape":2}</code>, <code>{"etape":3}</code> — dans cet ordre.
<em>Laissez-les dans la file.</em>`,
		Concepts: []string{"FIFO", "MessageGroupId", "Déduplication", "Exactly-once processing"},
		Docs:     []Link{{"AWS — Files FIFO", "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/FIFO-queues.html"}},
		Steps: []Step{
			{
				Title:   "Créer une file FIFO",
				Explain: `<p>Une file FIFO se reconnaît à son nom, qui doit obligatoirement se terminer par <code>.fifo</code>, et à un attribut dédié posé à la création.</p>`,
				Hints: []string{
					"Deux attributs à fixer à la création : l'un déclare la file comme FIFO, l'autre active la déduplication par contenu.",
					"Leurs noms : <code>FifoQueue</code> et <code>ContentBasedDeduplication</code>, valeurs <code>true</code>.",
				},
			},
			{
				Title:   "Envoyer dans un groupe",
				Explain: `<p>Dans une file FIFO, chaque message <strong>doit</strong> appartenir à un groupe. Avec la déduplication par contenu, deux corps identiques envoyés à moins de 5 minutes d'intervalle ne font qu'un seul message.</p>`,
				Hints: []string{
					"Option de <code>send-message</code> : <code>--message-group-id</code>.",
					"Essayez d'envoyer deux fois <code>{\"etape\":1}</code> et observez le nombre de messages : c'est la déduplication en action.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			u, err := queueURL(ctx, c, queueFifo)
			if !r.ok("La file paiements.fifo existe", err == nil, errDetail(err)) {
				return r.pending("FIFO activé", "Déduplication par contenu", "Étapes 1, 2, 3 dans l'ordre (groupe client-42)")
			}
			a, _ := queueAttrs(ctx, c, u)
			r.ok("FIFO activé", a["FifoQueue"] == "true", "FifoQueue = %q", a["FifoQueue"])
			r.ok("Déduplication par contenu", a["ContentBasedDeduplication"] == "true", "ContentBasedDeduplication = %q", a["ContentBasedDeduplication"])
			msgs, err := peek(ctx, c, u)
			var seq []int
			for _, m := range msgs {
				if m.Attributes[string(types.MessageSystemAttributeNameMessageGroupId)] != "client-42" {
					continue
				}
				var doc struct {
					Etape int `json:"etape"`
				}
				if json.Unmarshal([]byte(aws.ToString(m.Body)), &doc) == nil && doc.Etape > 0 {
					seq = append(seq, doc.Etape)
				}
			}
			ordered := len(seq) >= 3
			for i := 1; i < len(seq); i++ {
				if seq[i] < seq[i-1] {
					ordered = false
				}
			}
			has := map[int]bool{}
			for _, s := range seq {
				has[s] = true
			}
			r.ok("Étapes 1, 2, 3 dans l'ordre (groupe client-42)", err == nil && ordered && has[1] && has[2] && has[3], "séquence reçue dans le groupe client-42 : %v", seq)
			return r.items
		},
	}
}
