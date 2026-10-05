package game

import (
	"sort"
	"strings"
	"time"

	"cloudquest/internal/cloud"
)

// Mode « plateforme partagée » : une seule instance du jeu et des émulateurs
// sert toute une classe. Chaque étudiant·e travaille sur ses propres
// ressources, distinguées par un suffixe (livrexpress-factures-amina,
// Commandes-amina, commandes-entrantes-amina…).

// resourceNames liste les noms à personnaliser, les plus longs d'abord pour
// qu'un nom ne soit jamais remplacé à l'intérieur d'un autre.
var resourceNames = []string{
	queueCommandesDLQ, queuePaiementDLQ, queueCommandes, queuePaiement, queueMystere, queueFifo,
	bucketFactures, bucketArchives, bucketSite,
	tableHistorique, tableSessions, tableLivraisons,
}

// « Commandes » est aussi un mot courant du récit : le nom de la table n'est
// remplacé que là où il désigne sans ambiguïté la table.
var commandesContexts = []string{
	"<code>%s</code>", "table %s ", "--table-name %s", "[DynamoDB %s]",
}

// Personalize adapte une page HTML à l'étudiant·e : noms de ressources
// suffixés et adresses publiques des services. Sans suffixe ni adresses
// publiques (usage local), la page est renvoyée telle quelle.
func Personalize(html string, c *cloud.Clients) string {
	var pairs []string
	if c.Cfg.Hosted {
		for local, public := range map[string]string{
			"http://127.0.0.1:9100": c.Cfg.S3Public,
			"http://127.0.0.1:8100": c.Cfg.DynamoPublic,
			"http://127.0.0.1:9324": c.Cfg.SQSPublic,
		} {
			pairs = append(pairs, local, public)
		}
	}
	if c.Suffix == "" {
		if len(pairs) == 0 {
			return html
		}
		return strings.NewReplacer(pairs...).Replace(html)
	}
	// Un message d'erreur d'API ou une réponse saisie peut déjà contenir un
	// nom suffixé : on revient d'abord aux noms de base pour ne pas doubler
	// le suffixe.
	var undo []string
	for _, n := range resourceNames {
		undo = append(undo, c.N(n), n)
		pairs = append(pairs, n, c.N(n))
	}
	undo = append(undo, c.N(tableCommandes), tableCommandes)
	for _, ctx := range commandesContexts {
		pairs = append(pairs, strings.Replace(ctx, "%s", tableCommandes, 1), strings.Replace(ctx, "%s", c.N(tableCommandes), 1))
	}
	pairs = append(pairs, "%SUFFIXE%", c.Suffix)
	return strings.NewReplacer(pairs...).Replace(strings.NewReplacer(undo...).Replace(html))
}

// ApplyHosted remplace les consignes propres à l'installation locale
// (ports, docker compose) par celles de la plateforme partagée.
func (cat *Catalog) ApplyHosted() {
	const creds = "Les adresses et les identifiants sont dans l'encadré <strong>🔑 Accès et identifiants</strong> de cette page (et en bas du tableau de bord)."
	set := func(id string, step int, s Step) {
		if c := cat.Challenge(id); c != nil && step < len(c.Steps) {
			c.Steps[step] = s
		}
	}
	set("s3-bucket", 0, Step{
		Title: "Choisir son outil et se connecter",
		Explain: `<p>Ici, MinIO tourne sur la plateforme de la classe : vous vous y connectez depuis votre poste, comme on se connecte au vrai AWS. Trois outils possibles — tous valables :</p>
<ul><li>la <strong>console web</strong> de MinIO (rien à installer) ;</li>
<li>le client <strong>mc</strong> (MinIO Client) ;</li>
<li>l'<strong>AWS CLI</strong> officielle (c'est elle que vous utiliserez sur le vrai AWS).</li></ul>
<p>La plateforme est partagée : <strong>toutes vos ressources portent votre suffixe personnel</strong>, déjà inclus dans les noms affichés sur cette page. Sur AWS aussi, un nom de bucket doit être unique au monde.</p>`,
		Hints: []string{
			creds,
			"AWS CLI : fournissez la clé d'accès, la clé secrète et la région (commande <code>aws configure</code>, ou variables <code>AWS_ACCESS_KEY_ID</code>, <code>AWS_SECRET_ACCESS_KEY</code>, <code>AWS_DEFAULT_REGION</code>).",
			"Avec l'AWS CLI, chaque commande a besoin de l'option <code>--endpoint-url</code> suivie de l'adresse S3 de la plateforme. Sans elle, la CLI essaie de joindre le vrai AWS !",
		},
	})
	set("dyn-table", 0, Step{
		Title: "Parler à DynamoDB Local",
		Explain: `<p>DynamoDB Local expose exactement la même API que le vrai service. L'outil de référence est
l'<strong>AWS CLI</strong> (<code>aws dynamodb …</code>) ; vous pouvez aussi écrire un petit programme Go avec le SDK.
Vos tables portent votre suffixe personnel, comme vos buckets.</p>`,
		Hints: []string{
			"Chaque commande a besoin de <code>--endpoint-url</code> suivie de l'adresse DynamoDB de la plateforme. " + creds,
			"Utilisez la même clé d'accès que pour S3 : la plateforme refuse les requêtes signées avec une autre clé.",
			"<code>aws dynamodb list-tables</code> est une bonne commande pour tester la connexion (vous y verrez aussi les tables de vos camarades).",
		},
	})
	set("sqs-queue", 0, Step{
		Title: "Parler à ElasticMQ",
		Explain: `<p>ElasticMQ implémente l'API SQS : vous utilisez donc <code>aws sqs …</code> exactement comme sur AWS.
Vos files portent votre suffixe personnel.</p>`,
		Hints: []string{
			"Chaque commande a besoin de <code>--endpoint-url</code> suivie de l'adresse SQS de la plateforme. " + creds,
			"Les commandes SQS manipulent surtout des <strong>URL de file</strong>, pas des noms : <code>get-queue-url</code> fait la conversion.",
		},
	})
	if c := cat.Challenge("s3-presign"); c != nil && len(c.Steps) > 1 && len(c.Steps[1].Hints) > 2 {
		c.Steps[1].Hints[2] = "L'hôte de l'URL doit être l'adresse S3 de la plateforme, celle que vous passez à <code>--endpoint-url</code> : la signature dépend aussi de l'hôte !"
	}
	if ch := cat.Chapter("projet"); ch != nil {
		ch.Intro += `<p>Le cahier de projet est aussi <a href="/docs/cahier-de-projet.pdf" target="_blank" rel="noopener">téléchargeable ici (PDF)</a> ;
il a été rédigé pour une installation locale : sur la plateforme, ajoutez votre suffixe aux noms qu'il cite.</p>
<p><strong>Sur la plateforme partagée</strong>, récupérez le squelette avec
<code>git clone https://github.com/raezon/lab_data_quest</code>, puis indiquez au worker où se trouvent
les services et quel est votre suffixe avant de le lancer :</p>
<pre><code>export CQ_S3_ENDPOINT=http://127.0.0.1:9100
export CQ_DYNAMO_ENDPOINT=http://127.0.0.1:8100
export CQ_SQS_ENDPOINT=http://127.0.0.1:9324
export CQ_ACCESS_KEY=…   CQ_SECRET_KEY=…     # voir « Accès et identifiants »
export CQ_SUFFIXE=%SUFFIXE%
go run ./projet/pipeline</code></pre>`
	}
}

// --- Suivi par le formateur --------------------------------------------------

// ChapterScore résume la progression d'un joueur sur un chapitre.
type ChapterScore struct {
	Chapter   *Chapter
	Done      int
	Total     int
	XP        int
	QuizRight int
	QuizDone  int
	QuizTotal int
}

// ChallengeScore détaille un défi pour un joueur.
type ChallengeScore struct {
	Challenge *Challenge
	State     ChallengeState
	Started   bool
}

// PlayerScore est la fiche complète d'un joueur.
type PlayerScore struct {
	Key        string
	Player     *Player
	Pos        int
	XP         int
	Pct        int
	Rank       Rank
	Done       int
	Total      int
	Hints      int
	Attempts   int
	QuizRight  int
	QuizDone   int
	QuizTotal  int
	Chapters   []ChapterScore
	Challenges []ChallengeScore
	Last       time.Time
}

// ChallengeStat agrège un défi sur toute la classe.
type ChallengeStat struct {
	Challenge   *Challenge
	Started     int
	Done        int
	Pct         int // part des joueurs ayant réussi
	AvgHints    float64
	AvgAttempts float64
	AvgXP       int
}

// Scores renvoie la fiche de chaque joueur (classée par XP décroissant) et
// les statistiques par défi.
func (s *Store) Scores(cat *Catalog) ([]PlayerScore, []ChallengeStat) {
	s.mu.Lock()
	defer s.mu.Unlock()
	maxXP := cat.MaxXP()
	rows := make([]PlayerScore, 0, len(s.Players))
	stats := map[string]*ChallengeStat{}
	var order []*ChallengeStat
	for _, ch := range cat.Chapters {
		for _, c := range ch.Challenges {
			st := &ChallengeStat{Challenge: c}
			stats[c.ID] = st
			order = append(order, st)
		}
	}
	for key, p := range s.Players {
		row := PlayerScore{Key: key, Player: p, XP: p.XP(), Last: p.LastSeen}
		row.Rank, _ = RankFor(row.XP)
		if maxXP > 0 {
			row.Pct = row.XP * 100 / maxXP
		}
		for _, ch := range cat.Chapters {
			cs := ChapterScore{Chapter: ch, Total: len(ch.Challenges), QuizTotal: len(ch.Quiz)}
			for _, c := range ch.Challenges {
				st, started := p.Challenges[c.ID]
				d := ChallengeScore{Challenge: c}
				if started {
					d.State, d.Started = *st, st.Attempts > 0 || st.HintCount() > 0 || st.Done
					row.Hints += st.HintCount()
					row.Attempts += st.Attempts
					if st.DoneAt.After(row.Last) {
						row.Last = st.DoneAt
					}
				}
				if d.Started {
					agg := stats[c.ID]
					agg.Started++
					agg.AvgHints += float64(st.HintCount())
					agg.AvgAttempts += float64(st.Attempts)
					if st.Done {
						agg.Done++
						agg.AvgXP += st.XPEarned
						cs.Done++
						cs.XP += st.XPEarned
					}
				}
				row.Challenges = append(row.Challenges, d)
			}
			for _, q := range ch.Quiz {
				if a, ok := p.Quiz[q.ID]; ok {
					cs.QuizDone++
					if a.Correct {
						cs.QuizRight++
						cs.XP += QuizXP
					}
				}
			}
			row.Done += cs.Done
			row.Total += cs.Total
			row.QuizRight += cs.QuizRight
			row.QuizDone += cs.QuizDone
			row.QuizTotal += cs.QuizTotal
			row.Chapters = append(row.Chapters, cs)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].XP != rows[j].XP {
			return rows[i].XP > rows[j].XP
		}
		return rows[i].Player.Name < rows[j].Player.Name
	})
	for i := range rows {
		rows[i].Pos = i + 1
		if i > 0 && rows[i].XP == rows[i-1].XP {
			rows[i].Pos = rows[i-1].Pos // ex æquo
		}
	}
	out := make([]ChallengeStat, 0, len(order))
	for _, st := range order {
		if st.Started > 0 {
			st.AvgHints /= float64(st.Started)
			st.AvgAttempts /= float64(st.Started)
		}
		if st.Done > 0 {
			st.AvgXP /= st.Done
		}
		if len(rows) > 0 {
			st.Pct = st.Done * 100 / len(rows)
		}
		out = append(out, *st)
	}
	return rows, out
}
