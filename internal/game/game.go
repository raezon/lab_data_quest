// Package game décrit les chapitres, les défis, les indices progressifs,
// les quiz et le système de points de CloudQuest.
package game

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/smithy-go"

	"cloudquest/internal/cloud"
)

// Step est une sous-étape guidée d'un défi. Elle explique CE QU'IL FAUT
// obtenir et propose des indices de plus en plus précis, sans jamais
// donner la solution complète.
type Step struct {
	Title   string
	Explain string   // HTML de confiance (contenu pédagogique)
	Hints   []string // HTML de confiance, du plus vague au plus précis
}

// InputSpec décrit une réponse que l'étudiant doit saisir.
type InputSpec struct {
	Label       string
	Placeholder string
}

// SetupSpec décrit une action préparatoire déclenchée par l'étudiant
// (injection de données, simulation d'incident…).
type SetupSpec struct {
	Label string
	Run   func(ctx context.Context, c *cloud.Clients, st *ChallengeState) (string, error)
}

// CheckItem est un point de contrôle affiché à l'étudiant.
type CheckItem struct {
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// CheckFunc vérifie l'état réel des services.
type CheckFunc func(ctx context.Context, c *cloud.Clients, answer string, st *ChallengeState) []CheckItem

// Link pointe vers une documentation officielle.
type Link struct {
	Label string
	URL   string
}

// Challenge est un défi (une "mission").
type Challenge struct {
	ID        string
	Title     string
	Story     string // HTML
	Objective string // HTML
	XP        int
	Concepts  []string
	Docs      []Link
	Steps     []Step
	Input     *InputSpec
	Setup     *SetupSpec
	Check     CheckFunc

	Chapter *Chapter `json:"-"`
	Index   int
}

// TotalHints renvoie le nombre total d'indices du défi.
func (c *Challenge) TotalHints() int {
	n := 0
	for _, s := range c.Steps {
		n += len(s.Hints)
	}
	return n
}

// Question est une question de quiz à choix multiple.
type Question struct {
	ID      string
	Text    string
	Options []string
	Answer  int
	Explain string
}

// Chapter regroupe des défis autour d'un service.
type Chapter struct {
	ID         string
	Title      string
	Service    string // service AWS réel
	LocalTool  string // émulateur local
	Icon       string
	Color      string
	Intro      string // HTML
	Challenges []*Challenge
	Quiz       []Question
	// RequiresAll : le chapitre ne s'ouvre qu'une fois ces chapitres terminés.
	RequiresAll []string
}

// Catalog est l'ensemble du contenu du jeu.
type Catalog struct {
	Chapters   []*Chapter
	challenges map[string]*Challenge
	questions  map[string]*Question
}

// NewCatalog assemble les chapitres.
func NewCatalog() *Catalog {
	cat := &Catalog{
		Chapters:   []*Chapter{s3Chapter(), dynamoChapter(), sqsChapter(), projectChapter()},
		challenges: map[string]*Challenge{},
		questions:  map[string]*Question{},
	}
	for _, ch := range cat.Chapters {
		for i, c := range ch.Challenges {
			c.Chapter, c.Index = ch, i
			if _, dup := cat.challenges[c.ID]; dup {
				panic("défi en double: " + c.ID)
			}
			cat.challenges[c.ID] = c
		}
		for i := range ch.Quiz {
			q := &ch.Quiz[i]
			cat.questions[q.ID] = q
		}
	}
	return cat
}

// Challenge renvoie un défi par identifiant.
func (cat *Catalog) Challenge(id string) *Challenge { return cat.challenges[id] }

// Question renvoie une question par identifiant.
func (cat *Catalog) Question(id string) *Question { return cat.questions[id] }

// Chapter renvoie un chapitre par identifiant.
func (cat *Catalog) Chapter(id string) *Chapter {
	for _, ch := range cat.Chapters {
		if ch.ID == id {
			return ch
		}
	}
	return nil
}

// MaxXP renvoie le total de points disponibles.
func (cat *Catalog) MaxXP() int {
	t := 0
	for _, ch := range cat.Chapters {
		for _, c := range ch.Challenges {
			t += c.XP
		}
		t += len(ch.Quiz) * QuizXP
	}
	return t
}

// --- Règles de score -------------------------------------------------------

const (
	// QuizXP est le gain d'une bonne réponse au premier essai.
	QuizXP = 20
	// HintPenaltyPct est la pénalité par indice révélé (en % des XP du défi).
	HintPenaltyPct = 10
	// MinXPPct est le plancher de points d'un défi réussi.
	MinXPPct = 40
)

// EarnedXP calcule les points d'un défi selon le nombre d'indices utilisés.
func EarnedXP(c *Challenge, hintsUsed int) int {
	pct := 100 - hintsUsed*HintPenaltyPct
	if pct < MinXPPct {
		pct = MinXPPct
	}
	return c.XP * pct / 100
}

// Rank est un grade du joueur.
type Rank struct {
	Name  string
	MinXP int
	Icon  string
}

// Ranks liste les grades, du plus bas au plus haut.
var Ranks = []Rank{
	{"Stagiaire Cloud", 0, "🎒"},
	{"Apprenti·e Stockage", 300, "📦"},
	{"Technicien·ne Data", 900, "🛠️"},
	{"Ingénieur·e Cloud", 1800, "⚙️"},
	{"Architecte Solutions", 2800, "🏛️"},
	{"Légende LivrExpress", 3800, "🚀"},
}

// RankFor renvoie le grade courant et le suivant (nil si grade maximum).
func RankFor(xp int) (Rank, *Rank) {
	idx := 0
	for i, r := range Ranks {
		if xp >= r.MinXP {
			idx = i
		}
	}
	if idx+1 < len(Ranks) {
		next := Ranks[idx+1]
		return Ranks[idx], &next
	}
	return Ranks[idx], nil
}

// --- petits utilitaires pour écrire les vérifications ----------------------

type report struct{ items []CheckItem }

func (r *report) ok(label string, ok bool, detail string, args ...any) bool {
	// Le détail n'aide qu'en cas d'échec : on ne l'affiche pas sinon.
	if ok {
		detail = ""
	} else if len(args) > 0 {
		detail = fmt.Sprintf(detail, args...)
	}
	r.items = append(r.items, CheckItem{Label: label, OK: ok, Detail: detail})
	return ok
}

// pending ajoute des contrôles non évalués car un prérequis a échoué.
func (r *report) pending(labels ...string) []CheckItem {
	for _, l := range labels {
		r.items = append(r.items, CheckItem{Label: l, OK: false, Detail: "non vérifié : corrigez d'abord le point précédent"})
	}
	return r.items
}

func errDetail(err error) string {
	if err == nil {
		return ""
	}
	// Une erreur d'API porte un code court (NoSuchBucket, ResourceNotFoundException…).
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return strings.TrimSuffix(ae.ErrorCode()+" : "+ae.ErrorMessage(), " : ")
	}
	s := err.Error()
	// On garde la fin du message, plus parlante que la pile d'appels SDK.
	if i := strings.LastIndex(s, "api error "); i >= 0 {
		return s[i+len("api error "):]
	}
	if len(s) > 180 {
		s = s[len(s)-180:]
	}
	return s
}
