package game

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"cloudquest/internal/cloud"
)

const (
	tableCommandes  = "Commandes"
	tableLivraisons = "Livraisons"
	tableHistorique = "HistoriqueVentes"
	tableSessions   = "SessionsLivreurs"
	gsiStatut       = "statut-index"
)

var statutsValides = map[string]bool{"EN_ATTENTE": true, "EXPEDIEE": true, "LIVREE": true, "TRAITEE": true}

func dynamoChapter() *Chapter {
	return &Chapter{
		ID:        "dynamodb",
		Title:     "Chapitre 2 — Le registre éclair",
		Service:   "Amazon DynamoDB",
		LocalTool: "DynamoDB Local",
		Icon:      "⚡",
		Color:     "#4053d6",
		Intro: `<p>Le suivi des commandes tient dans un tableur partagé que 40 personnes modifient en même temps.
Il faut une base capable d'encaisser les pics du Ramadan et des soldes, sans serveur à gérer :
<strong>DynamoDB</strong>, la base NoSQL clé-valeur d'AWS. Vous utiliserez <strong>DynamoDB Local</strong>,
l'émulateur officiel fourni par AWS.</p>
<p>Notions clés : table, clé de partition, clé de tri, item, Query vs Scan, index secondaire global (GSI), TTL.</p>`,
		Challenges: []*Challenge{
			dynCreateTable(), dynPutItems(), dynCompositeKey(), dynGSI(), dynInvestigation(), dynTTL(),
		},
		Quiz: []Question{
			{ID: "dq1", Text: "Que doit-on obligatoirement définir à la création d'une table DynamoDB ?",
				Options: []string{"Tous les attributs de tous les items", "Uniquement la clé primaire (partition, et éventuellement tri)", "Un schéma SQL", "Le nombre de serveurs"},
				Answer:  1, Explain: "DynamoDB est <em>schemaless</em> en dehors de la clé : chaque item peut avoir des attributs différents. Seuls les attributs de clé (table et index) sont déclarés."},
			{ID: "dq2", Text: "Quelle est la différence principale entre <code>Query</code> et <code>Scan</code> ?",
				Options: []string{"Aucune, ce sont des alias", "Query cible une clé de partition ; Scan lit toute la table", "Scan est plus rapide", "Query ne fonctionne que sur les index"},
				Answer:  1, Explain: "Query lit uniquement les items d'une partition (coût proportionnel au résultat). Scan parcourt toute la table puis filtre : coûteux et lent à grande échelle."},
			{ID: "dq3", Text: "Avec la clé (livreurId, dateLivraison), quelle requête est efficace ?",
				Options: []string{"Toutes les livraisons du 12 octobre, tous livreurs confondus", "Les livraisons du livreur LIV-007 en octobre 2026", "Les livraisons dont le montant dépasse 5000", "Les livreurs basés à Oran"},
				Answer:  1, Explain: "On fixe la clé de partition (LIV-007) et on filtre la clé de tri par plage ou par préfixe (<code>begins_with(dateLivraison, '2026-10')</code>)."},
			{ID: "dq4", Text: "À quoi sert un index secondaire global (GSI) ?",
				Options: []string{"À sauvegarder la table", "À interroger la table selon une autre clé que la clé primaire", "À chiffrer les données", "À accélérer les Scan"},
				Answer:  1, Explain: "Un GSI maintient une copie projetée des items, organisée selon une autre clé (ex. <code>statut</code>), ce qui permet des Query efficaces sur ce critère."},
			{ID: "dq5", Text: "Le TTL DynamoDB attend un attribut contenant…",
				Options: []string{"Une date ISO 8601 en texte", "Un horodatage Unix en secondes (type Number)", "Une durée en minutes", "Un booléen"},
				Answer:  1, Explain: "L'attribut TTL doit être un <strong>Number</strong> représentant un instant Unix (secondes). Les items expirés sont supprimés en tâche de fond, sans coût d'écriture."},
			{ID: "dq6", Text: "Pourquoi éviter une clé de partition comme <code>pays</code> pour une appli 100 % algérienne ?",
				Options: []string{"Le mot est réservé", "Toutes les écritures iraient dans la même partition (« hot partition »)", "Les clés doivent être numériques", "Ce n'est pas un problème"},
				Answer:  1, Explain: "Une bonne clé de partition a une forte cardinalité et répartit uniformément le trafic. Une seule valeur concentre toute la charge sur une partition."},
		},
	}
}

func describeTable(ctx context.Context, c *cloud.Clients, name string) (*types.TableDescription, error) {
	out, err := c.Dynamo.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(name)})
	if err != nil {
		return nil, err
	}
	return out.Table, nil
}

// keyOf renvoie le nom et le type des attributs HASH / RANGE.
func keyOf(ks []types.KeySchemaElement, defs []types.AttributeDefinition) (hash, hashT, rng, rngT string) {
	typ := map[string]string{}
	for _, d := range defs {
		typ[aws.ToString(d.AttributeName)] = string(d.AttributeType)
	}
	for _, k := range ks {
		n := aws.ToString(k.AttributeName)
		if k.KeyType == types.KeyTypeHash {
			hash, hashT = n, typ[n]
		} else {
			rng, rngT = n, typ[n]
		}
	}
	return
}

func scanAll(ctx context.Context, c *cloud.Clients, table string) ([]map[string]types.AttributeValue, error) {
	var items []map[string]types.AttributeValue
	p := dynamodb.NewScanPaginator(c.Dynamo, &dynamodb.ScanInput{TableName: aws.String(table)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
	}
	return items, nil
}

func attrS(it map[string]types.AttributeValue, k string) (string, bool) {
	v, ok := it[k].(*types.AttributeValueMemberS)
	if !ok {
		return "", false
	}
	return v.Value, true
}

func attrN(it map[string]types.AttributeValue, k string) (float64, bool) {
	v, ok := it[k].(*types.AttributeValueMemberN)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(v.Value, 64)
	return f, err == nil
}

var stepConnectDynamo = Step{
	Title: "Parler à DynamoDB Local",
	Explain: `<p>DynamoDB Local expose exactement la même API que le vrai service. L'outil de référence est
l'<strong>AWS CLI</strong> (<code>aws dynamodb …</code>) ; vous pouvez aussi écrire un petit programme Go avec le SDK.</p>`,
	Hints: []string{
		"L'émulateur écoute sur le port <strong>8100</strong> : chaque commande a besoin de <code>--endpoint-url http://127.0.0.1:8100</code>.",
		"DynamoDB Local accepte n'importe quels identifiants, mais il en faut quand même : ceux du conteneur <code>aws</code> de Docker Compose conviennent.",
		"<code>aws dynamodb list-tables</code> est une bonne commande pour tester la connexion.",
	},
}

// --- Défi 1 ----------------------------------------------------------------

func dynCreateTable() *Challenge {
	return &Challenge{
		ID: "dyn-table", Title: "Le registre des commandes", XP: 100,
		Story: `<p>Yasmine, responsable des opérations : « Chaque commande a un identifiant unique du type
<code>CMD-2026-00042</code>. C'est tout ce dont on a besoin pour la retrouver. »</p>`,
		Objective: `Créer la table <code>Commandes</code> avec une clé de partition <code>commandeId</code> de type <strong>chaîne</strong>, sans clé de tri.`,
		Concepts:  []string{"Table", "Clé de partition", "Mode de capacité"},
		Docs:      []Link{{"AWS — Composants de DynamoDB", "https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/HowItWorks.CoreComponents.html"}},
		Steps: []Step{
			stepConnectDynamo,
			{
				Title:   "Définir la clé primaire",
				Explain: `<p>À la création, on déclare uniquement les attributs qui servent de clé : leur <strong>nom</strong>, leur <strong>type</strong> (S = chaîne, N = nombre, B = binaire) et leur <strong>rôle</strong> (HASH = partition, RANGE = tri).</p>`,
				Hints: []string{
					"<code>aws dynamodb create-table</code> a besoin de <code>--attribute-definitions</code> et de <code>--key-schema</code>.",
					"Le format court d'un attribut est <code>AttributeName=…,AttributeType=…</code> ; celui de la clé <code>AttributeName=…,KeyType=…</code>.",
					"Il faut aussi choisir un mode de facturation : le mode « à la demande » (<code>--billing-mode PAY_PER_REQUEST</code>) évite de régler des capacités.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			t, err := describeTable(ctx, c, c.N(tableCommandes))
			if !r.ok("La table Commandes existe", err == nil, errDetail(err)) {
				return r.pending("Clé de partition commandeId (S)", "Pas de clé de tri")
			}
			h, ht, rg, _ := keyOf(t.KeySchema, t.AttributeDefinitions)
			r.ok("Clé de partition commandeId (S)", h == "commandeId" && ht == "S", "clé actuelle : %s (%s)", h, ht)
			r.ok("Pas de clé de tri", rg == "", "clé de tri trouvée : %s", rg)
			return r.items
		},
	}
}

// --- Défi 2 ----------------------------------------------------------------

func dynPutItems() *Challenge {
	return &Challenge{
		ID: "dyn-items", Title: "Les premières commandes", XP: 120,
		Story: `<p>Le site e-commerce va bientôt écrire directement dans la table. Avant cela, Yasmine veut
voir quelques commandes réalistes pour valider le format.</p>`,
		Objective: `Insérer <strong>au moins 5</strong> commandes dans <code>Commandes</code>. Chaque item doit avoir :
<code>client</code> (S), <code>montant</code> (N) et <code>statut</code> (S) parmi <code>EN_ATTENTE</code>, <code>EXPEDIEE</code>, <code>LIVREE</code>.
Utilisez <strong>au moins 2 statuts différents</strong>.`,
		Concepts: []string{"Item", "Attribute value", "PutItem", "BatchWriteItem"},
		Docs:     []Link{{"AWS — Utiliser les items", "https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/WorkingWithItems.html"}},
		Steps: []Step{
			{
				Title:   "Comprendre le format des valeurs",
				Explain: `<p>L'API DynamoDB est explicite sur les types : chaque valeur est enveloppée dans un objet qui indique son type. Par exemple une chaîne s'écrit <code>{"S": "…"}</code>.</p>`,
				Hints: []string{
					"Un nombre s'écrit lui aussi avec des guillemets, mais sous la clé <code>N</code>. Oui, c'est surprenant : c'est pour garder la précision.",
					"N'oubliez pas la clé primaire <code>commandeId</code> dans chaque item, sinon l'écriture est refusée.",
				},
			},
			{
				Title:   "Insérer efficacement",
				Explain: `<p>On peut insérer item par item, ou par lot de 25 maximum.</p>`,
				Hints: []string{
					"<code>aws dynamodb put-item</code> prend <code>--table-name</code> et <code>--item</code> (un JSON, ou <code>file://item.json</code>).",
					"Pour un lot : <code>batch-write-item --request-items file://lot.json</code>. La structure regroupe des <code>PutRequest</code> par nom de table.",
					"Contrôlez votre travail avec <code>aws dynamodb scan --table-name Commandes</code> (ajoutez <code>--select COUNT</code> pour juste compter).",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			items, err := scanAll(ctx, c, c.N(tableCommandes))
			if !r.ok("La table Commandes est lisible", err == nil, errDetail(err)) {
				return r.pending("≥ 5 commandes bien formées", "≥ 2 statuts différents")
			}
			valid, st := 0, map[string]bool{}
			for _, it := range items {
				cl, ok1 := attrS(it, "client")
				m, ok2 := attrN(it, "montant")
				s, ok3 := attrS(it, "statut")
				if ok1 && cl != "" && ok2 && m >= 0 && ok3 && statutsValides[s] {
					valid++
					st[s] = true
				}
			}
			r.ok("≥ 5 commandes bien formées", valid >= 5, "%d item(s) valide(s) sur %d — vérifiez les types (S/N) et les statuts", valid, len(items))
			r.ok("≥ 2 statuts différents", len(st) >= 2, "statut(s) utilisé(s) : %d", len(st))
			return r.items
		},
	}
}

// --- Défi 3 ----------------------------------------------------------------

func dynCompositeKey() *Challenge {
	return &Challenge{
		ID: "dyn-composite", Title: "La tournée du livreur", XP: 150,
		Story: `<p>Chaque soir, les chefs d'équipe veulent voir l'historique des tournées d'un livreur,
trié par date. Avec une simple clé de partition, impossible de trier…</p>`,
		Objective: `Créer la table <code>Livraisons</code> avec la clé de partition <code>livreurId</code> (S) et la clé de tri <code>dateLivraison</code> (S, format <code>AAAA-MM-JJ</code>).
Y insérer <strong>au moins 3</strong> livraisons pour le livreur <code>LIV-007</code>.`,
		Concepts: []string{"Clé composite", "Clé de tri", "Query", "KeyConditionExpression"},
		Docs:     []Link{{"AWS — Bonnes pratiques clé de tri", "https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/bp-sort-keys.html"}},
		Steps: []Step{
			{
				Title:   "Créer une clé composite",
				Explain: `<p>Avec une clé composite, plusieurs items partagent la même clé de partition et sont <strong>triés</strong> par la clé de tri. Le couple (partition, tri) doit être unique.</p>`,
				Hints: []string{
					"Même commande que pour <code>Commandes</code>, mais avec deux attributs déclarés et deux éléments dans le key-schema.",
					"Le rôle de la clé de tri s'appelle <code>RANGE</code>.",
				},
			},
			{
				Title:   "Interroger par plage",
				Explain: `<p>Pourquoi un format de date <code>AAAA-MM-JJ</code> ? Parce que l'ordre alphabétique d'une telle chaîne est aussi l'ordre chronologique.</p>`,
				Hints: []string{
					"Insérez 3 items avec <code>livreurId = LIV-007</code> et trois dates différentes (sinon ils s'écrasent !).",
					"Testez une <code>query</code> avec <code>--key-condition-expression</code> et des valeurs passées via <code>--expression-attribute-values</code>.",
					"Une condition sur la clé de tri peut utiliser <code>BETWEEN</code> ou <code>begins_with</code>.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			t, err := describeTable(ctx, c, c.N(tableLivraisons))
			if !r.ok("La table Livraisons existe", err == nil, errDetail(err)) {
				return r.pending("Clé de partition livreurId (S)", "Clé de tri dateLivraison (S)", "≥ 3 livraisons pour LIV-007")
			}
			h, ht, rg, rt := keyOf(t.KeySchema, t.AttributeDefinitions)
			okH := r.ok("Clé de partition livreurId (S)", h == "livreurId" && ht == "S", "clé actuelle : %s (%s)", h, ht)
			okR := r.ok("Clé de tri dateLivraison (S)", rg == "dateLivraison" && rt == "S", "clé de tri actuelle : %q (%s)", rg, rt)
			if !okH || !okR {
				return r.pending("≥ 3 livraisons pour LIV-007")
			}
			out, err := c.Dynamo.Query(ctx, &dynamodb.QueryInput{
				TableName:                 aws.String(c.N(tableLivraisons)),
				KeyConditionExpression:    aws.String("livreurId = :l"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":l": &types.AttributeValueMemberS{Value: "LIV-007"}},
				Select:                    types.SelectCount,
			})
			n := int32(0)
			if err == nil {
				n = out.Count
			}
			r.ok("≥ 3 livraisons pour LIV-007", n >= 3, "%d trouvée(s)", n)
			return r.items
		},
	}
}

// --- Défi 4 ----------------------------------------------------------------

func dynGSI() *Challenge {
	return &Challenge{
		ID: "dyn-gsi", Title: "Trouver les retardataires", XP: 150,
		Story: `<p>Le centre d'appels reçoit des réclamations : « Ma commande est en attente depuis une semaine ! ».
Yasmine veut lister instantanément toutes les commandes <code>EN_ATTENTE</code>… sans lire toute la table.</p>`,
		Objective: `Ajouter à <code>Commandes</code> un index secondaire global nommé <code>statut-index</code> dont la clé de partition est <code>statut</code> (S).
Puis interrogez-le et indiquez combien de commandes sont <code>EN_ATTENTE</code> dans votre table.`,
		Concepts: []string{"GSI", "Projection", "Query sur index", "Modélisation par accès"},
		Docs:     []Link{{"AWS — Index secondaires globaux", "https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/GSI.html"}},
		Input:    &InputSpec{Label: "Nombre de commandes EN_ATTENTE", Placeholder: "ex. 2"},
		Steps: []Step{
			{
				Title:   "Ajouter un index à une table existante",
				Explain: `<p>Un GSI peut être créé après coup. Il faut déclarer la nouvelle clé, choisir une <strong>projection</strong> (quels attributs copier dans l'index) et lui donner un nom.</p>`,
				Hints: []string{
					"Cherchez dans <code>aws dynamodb update-table help</code> l'option <code>--global-secondary-index-updates</code>.",
					"L'attribut <code>statut</code> doit aussi apparaître dans <code>--attribute-definitions</code> de la même commande.",
					"Une mise à jour d'index contient une action <code>Create</code> avec <code>IndexName</code>, <code>KeySchema</code> et <code>Projection</code> (par ex. <code>ProjectionType: ALL</code>).",
				},
			},
			{
				Title:   "Interroger l'index",
				Explain: `<p>Une Query sur un index ressemble à une Query sur la table : on précise simplement le nom de l'index.</p>`,
				Hints: []string{
					"L'option <code>--index-name</code> de <code>aws dynamodb query</code>.",
					"Attention : <code>statut</code> n'est pas un mot réservé, mais <code>status</code> l'est ! Dans le doute, utilisez <code>--expression-attribute-names</code>.",
					"Ajoutez <code>--select COUNT</code> pour obtenir directement le nombre.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, answer string, _ *ChallengeState) []CheckItem {
			r := &report{}
			t, err := describeTable(ctx, c, c.N(tableCommandes))
			if !r.ok("La table Commandes existe", err == nil, errDetail(err)) {
				return r.pending("Index statut-index présent", "Clé de l'index : statut (S)", "Bonne réponse au comptage")
			}
			var gsi *types.GlobalSecondaryIndexDescription
			for i, g := range t.GlobalSecondaryIndexes {
				if aws.ToString(g.IndexName) == gsiStatut {
					gsi = &t.GlobalSecondaryIndexes[i]
				}
			}
			if !r.ok("Index statut-index présent", gsi != nil, "%d index trouvé(s), aucun nommé statut-index", len(t.GlobalSecondaryIndexes)) {
				return r.pending("Clé de l'index : statut (S)", "Bonne réponse au comptage")
			}
			h, ht, _, _ := keyOf(gsi.KeySchema, t.AttributeDefinitions)
			if !r.ok("Clé de l'index : statut (S)", h == "statut" && ht == "S", "clé actuelle : %s (%s)", h, ht) {
				return r.pending("Bonne réponse au comptage")
			}
			out, err := c.Dynamo.Query(ctx, &dynamodb.QueryInput{
				TableName: aws.String(c.N(tableCommandes)), IndexName: aws.String(gsiStatut),
				KeyConditionExpression:    aws.String("statut = :s"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":s": &types.AttributeValueMemberS{Value: "EN_ATTENTE"}},
				Select:                    types.SelectCount,
			})
			if err != nil {
				r.ok("Bonne réponse au comptage", false, errDetail(err))
				return r.items
			}
			got, err := strconv.Atoi(strings.TrimSpace(answer))
			r.ok("Bonne réponse au comptage", err == nil && int32(got) == out.Count, "ce n'est pas le bon nombre : interrogez l'index plutôt que de compter à la main")
			return r.items
		},
	}
}

// --- Défi 5 ----------------------------------------------------------------

var (
	clientsHisto = []string{"Pharmacie El Amel", "Superette Nour", "Librairie Ibn Khaldoun", "Boulangerie Sidi Yahia",
		"Optique Tlemcen", "Quincaillerie Bab Ezzouar", "Fleuriste Jasmin", "Électro Constantine",
		"Pâtisserie Les Andalouses", "Café du Port", "Cosmétiques Lalla", "Sport Hydra"}
	villesHisto = []string{"Alger", "Oran", "Constantine", "Annaba", "Tlemcen", "Sétif", "Béjaïa"}
	moisFR      = []string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}
)

func dynInvestigation() *Challenge {
	return &Challenge{
		ID: "dyn-enquete", Title: "L'enquête comptable", XP: 200,
		Story: `<p>Le cabinet d'audit débarque. Il veut le chiffre d'affaires exact d'un client sur un mois précis,
tiré de l'historique des ventes. La table contient plusieurs centaines de lignes : à la main, c'est
impossible et risqué. À vous de trouver la bonne requête.</p>`,
		Objective: `Cliquez sur « Charger l'historique » : la table <code>HistoriqueVentes</code> (clé <code>client</code> + <code>dateVente</code> au format <code>AAAA-MM-JJ#n°</code>)
est créée et remplie. Une question d'audit personnalisée s'affiche alors : répondez-y avec un nombre entier.`,
		Concepts: []string{"Query", "begins_with", "Pagination", "Agrégation côté client"},
		Docs:     []Link{{"AWS — Query", "https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/Query.html"}},
		Input:    &InputSpec{Label: "Montant total (DA)", Placeholder: "ex. 154300"},
		Setup: &SetupSpec{
			Label: "📥 Charger l'historique des ventes",
			Run:   setupHistorique,
		},
		Steps: []Step{
			{
				Title:   "Explorer la table",
				Explain: `<p>Avant d'écrire une requête, regardez la forme des données : nom des attributs, format de la clé de tri.</p>`,
				Hints: []string{
					"<code>describe-table</code> donne la clé ; un <code>scan</code> limité (<code>--max-items 3</code>) montre des exemples d'items.",
					"La clé de tri ressemble à <code>2026-03-14#017</code> : la date d'abord, puis un numéro pour garantir l'unicité.",
				},
			},
			{
				Title:   "Cibler partition et mois",
				Explain: `<p>Le client est la clé de partition ; le mois est le <strong>début</strong> de la clé de tri. Une seule Query suffit, sans Scan.</p>`,
				Hints: []string{
					"Combinez dans <code>--key-condition-expression</code> une égalité sur la partition ET une condition sur le début de la clé de tri.",
					"Les noms de client contiennent des espaces et accents : passez les valeurs via un fichier JSON (<code>file://valeurs.json</code>) pour éviter les soucis de guillemets.",
				},
			},
			{
				Title:   "Faire la somme",
				Explain: `<p>DynamoDB ne fait pas de <code>SUM()</code> : l'agrégation se fait côté client (CLI + jq, ou un programme Go).</p>`,
				Hints: []string{
					"Avec <code>--query</code> (JMESPath) vous pouvez extraire la liste des montants : <code>Items[].montant.N</code>.",
					"<code>jq</code> sait convertir des chaînes en nombres (<code>tonumber</code>) et additionner (<code>add</code>).",
					"Si le résultat dépasse 1 Mo, la réponse est paginée : la CLI gère la pagination toute seule, un programme doit suivre <code>LastEvaluatedKey</code>.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, answer string, st *ChallengeState) []CheckItem {
			r := &report{}
			want, ok := st.Secret["total"]
			if !r.ok("Historique chargé", ok, "cliquez d'abord sur « Charger l'historique des ventes »") {
				return r.pending("Montant exact")
			}
			got := strings.NewReplacer(" ", "", "\u00a0", "", ".", "", ",", "", "DA", "", "da", "").Replace(answer)
			r.ok("Montant exact", got == want, "ce n'est pas le bon total. Vérifiez client, mois et que tous les items sont additionnés")
			return r.items
		},
	}
}

func setupHistorique(ctx context.Context, c *cloud.Clients, st *ChallengeState) (string, error) {
	// (Re)crée la table pour un jeu de données frais et propre à cet essai.
	_, _ = c.Dynamo.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(c.N(tableHistorique))})
	_, err := c.Dynamo.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:   aws.String(c.N(tableHistorique)),
		BillingMode: types.BillingModePayPerRequest,
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("client"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("dateVente"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("client"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("dateVente"), KeyType: types.KeyTypeRange},
		},
	})
	if err != nil {
		return "", fmt.Errorf("création de la table impossible : %s", errDetail(err))
	}
	target := clientsHisto[rand.IntN(len(clientsHisto))]
	month := 1 + rand.IntN(9)
	total := 0
	var batch []types.WriteRequest
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, err := c.Dynamo.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{RequestItems: map[string][]types.WriteRequest{c.N(tableHistorique): batch}})
		batch = batch[:0]
		return err
	}
	seq := 0
	for _, cl := range clientsHisto {
		n := 25 + rand.IntN(20)
		for i := 0; i < n; i++ {
			seq++
			m := 1 + rand.IntN(10)
			if cl == target && i < 6 {
				m = month // garantit plusieurs ventes sur le mois ciblé
			}
			d := fmt.Sprintf("2026-%02d-%02d#%03d", m, 1+rand.IntN(28), seq)
			amount := (5 + rand.IntN(400)) * 100
			if cl == target && m == month {
				total += amount
			}
			batch = append(batch, types.WriteRequest{PutRequest: &types.PutRequest{Item: map[string]types.AttributeValue{
				"client":    &types.AttributeValueMemberS{Value: cl},
				"dateVente": &types.AttributeValueMemberS{Value: d},
				"montant":   &types.AttributeValueMemberN{Value: strconv.Itoa(amount)},
				"ville":     &types.AttributeValueMemberS{Value: villesHisto[rand.IntN(len(villesHisto))]},
			}}})
			if len(batch) == 25 {
				if err := flush(); err != nil {
					return "", err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return "", err
	}
	st.Secret["total"] = strconv.Itoa(total)
	return fmt.Sprintf("Historique chargé (%d ventes). 🕵️ Question de l'auditeur : quel est le montant total des ventes du client « %s » en %s 2026 ?",
		seq, target, moisFR[month-1]), nil
}

// --- Défi 6 ----------------------------------------------------------------

func dynTTL() *Challenge {
	return &Challenge{
		ID: "dyn-ttl", Title: "Les données qui s'évaporent", XP: 150,
		Story: `<p>L'application mobile des livreurs crée une session à chaque connexion. Ces sessions
n'ont aucune valeur après 24 h, mais elles s'accumulent par millions. Supprimer en masse coûterait cher…
DynamoDB peut le faire gratuitement.</p>`,
		Objective: `Créer la table <code>SessionsLivreurs</code> (clé <code>sessionId</code>, S), y activer le <strong>TTL</strong> sur l'attribut <code>expireLe</code>,
et insérer au moins une session dont <code>expireLe</code> est un horodatage Unix <strong>dans le futur</strong>.`,
		Concepts: []string{"Time To Live", "Horodatage Unix", "Coût des suppressions"},
		Docs:     []Link{{"AWS — TTL", "https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/TTL.html"}},
		Steps: []Step{
			{
				Title:   "Activer le TTL",
				Explain: `<p>Le TTL est un réglage de la table qui désigne <strong>quel attribut</strong> contient la date d'expiration de chaque item.</p>`,
				Hints: []string{
					"La commande s'appelle <code>update-time-to-live</code> ; vérifiez ensuite avec <code>describe-time-to-live</code>.",
					"La spécification contient deux champs : un booléen d'activation et le nom de l'attribut.",
				},
			},
			{
				Title:   "Écrire une date d'expiration correcte",
				Explain: `<p>Le TTL n'accepte qu'un <strong>Number</strong> : le nombre de secondes depuis le 1er janvier 1970 (UTC).</p>`,
				Hints: []string{
					"Sous Linux, <code>date +%s</code> donne l'instant présent en secondes Unix.",
					"Dans 24 h : <code>date -d '+1 day' +%s</code>. Écrivez la valeur avec le type <code>N</code>, pas <code>S</code> !",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			t, err := describeTable(ctx, c, c.N(tableSessions))
			if !r.ok("La table SessionsLivreurs existe", err == nil, errDetail(err)) {
				return r.pending("Clé sessionId (S)", "TTL activé sur expireLe", "Une session expire dans le futur")
			}
			h, ht, _, _ := keyOf(t.KeySchema, t.AttributeDefinitions)
			r.ok("Clé sessionId (S)", h == "sessionId" && ht == "S", "clé actuelle : %s (%s)", h, ht)
			ttl, err := c.Dynamo.DescribeTimeToLive(ctx, &dynamodb.DescribeTimeToLiveInput{TableName: aws.String(c.N(tableSessions))})
			okTTL := err == nil && ttl.TimeToLiveDescription != nil &&
				ttl.TimeToLiveDescription.TimeToLiveStatus == types.TimeToLiveStatusEnabled &&
				aws.ToString(ttl.TimeToLiveDescription.AttributeName) == "expireLe"
			r.ok("TTL activé sur expireLe", okTTL, "TTL désactivé ou sur un autre attribut")
			items, err := scanAll(ctx, c, c.N(tableSessions))
			future, wrongType := false, false
			now := float64(time.Now().Unix())
			for _, it := range items {
				if v, ok := attrN(it, "expireLe"); ok && v > now && v < now+10*365*86400 {
					future = true
				}
				if _, ok := attrS(it, "expireLe"); ok {
					wrongType = true
				}
			}
			detail := "aucun item avec expireLe (Number) dans le futur"
			if wrongType {
				detail = "expireLe est stocké en texte (S) : le TTL l'ignore, il faut un Number (N)"
			}
			if err != nil {
				detail = errDetail(err)
			}
			r.ok("Une session expire dans le futur", future, "%s", detail)
			return r.items
		},
	}
}
