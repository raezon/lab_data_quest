package game

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ChallengeState est la progression d'un joueur sur un défi.
type ChallengeState struct {
	Done      bool              `json:"done"`
	DoneAt    time.Time         `json:"doneAt,omitempty"`
	XPEarned  int               `json:"xpEarned"`
	Attempts  int               `json:"attempts"`
	HintsUsed map[string]bool   `json:"hintsUsed,omitempty"` // clé "étape:indice"
	Secret    map[string]string `json:"secret,omitempty"`    // valeurs générées par Setup
	SetupInfo string            `json:"setupInfo,omitempty"` // message affiché après Setup
}

// HintCount renvoie le nombre d'indices révélés.
func (s *ChallengeState) HintCount() int { return len(s.HintsUsed) }

// QuizState est la réponse d'un joueur à une question.
type QuizState struct {
	Choice  int  `json:"choice"`
	Correct bool `json:"correct"`
}

// Player est un·e étudiant·e.
type Player struct {
	Name       string                     `json:"name"`
	Created    time.Time                  `json:"created"`
	Challenges map[string]*ChallengeState `json:"challenges"`
	Quiz       map[string]*QuizState      `json:"quiz"`
}

// State renvoie (en le créant si besoin) l'état d'un défi.
func (p *Player) State(id string) *ChallengeState {
	st, ok := p.Challenges[id]
	if !ok {
		st = &ChallengeState{HintsUsed: map[string]bool{}, Secret: map[string]string{}}
		p.Challenges[id] = st
	}
	if st.HintsUsed == nil {
		st.HintsUsed = map[string]bool{}
	}
	if st.Secret == nil {
		st.Secret = map[string]string{}
	}
	return st
}

// XP renvoie le total de points du joueur.
func (p *Player) XP() int {
	t := 0
	for _, s := range p.Challenges {
		if s.Done {
			t += s.XPEarned
		}
	}
	for _, q := range p.Quiz {
		if q.Correct {
			t += QuizXP
		}
	}
	return t
}

// IsDone indique si un défi est réussi.
func (p *Player) IsDone(id string) bool {
	s, ok := p.Challenges[id]
	return ok && s.Done
}

// ChapterDone indique si tous les défis d'un chapitre sont réussis.
func (p *Player) ChapterDone(ch *Chapter) bool {
	for _, c := range ch.Challenges {
		if !p.IsDone(c.ID) {
			return false
		}
	}
	return true
}

// Store persiste les joueurs dans un fichier JSON.
type Store struct {
	mu      sync.Mutex
	path    string
	Players map[string]*Player `json:"players"`
}

// OpenStore charge (ou crée) le fichier de progression.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, Players: map[string]*Player{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.Players == nil {
		s.Players = map[string]*Player{}
	}
	return s, nil
}

// Do exécute fn sous verrou et sauvegarde si fn renvoie save=true.
func (s *Store) Do(fn func() (save bool, err error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	save, err := fn()
	if err != nil || !save {
		return err
	}
	return s.flush()
}

func (s *Store) flush() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Player renvoie un joueur (à appeler sous verrou via Do) ou le crée.
func (s *Store) Player(name string) *Player {
	p, ok := s.Players[name]
	if !ok {
		p = &Player{Name: name, Created: time.Now(), Challenges: map[string]*ChallengeState{}, Quiz: map[string]*QuizState{}}
		s.Players[name] = p
	}
	if p.Quiz == nil {
		p.Quiz = map[string]*QuizState{}
	}
	if p.Challenges == nil {
		p.Challenges = map[string]*ChallengeState{}
	}
	return p
}

// LeaderRow est une ligne du classement.
type LeaderRow struct {
	Name string
	XP   int
	Done int
	Rank Rank
}

// Leaderboard renvoie le classement des joueurs.
func (s *Store) Leaderboard() []LeaderRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]LeaderRow, 0, len(s.Players))
	for _, p := range s.Players {
		done := 0
		for _, c := range p.Challenges {
			if c.Done {
				done++
			}
		}
		r, _ := RankFor(p.XP())
		rows = append(rows, LeaderRow{Name: p.Name, XP: p.XP(), Done: done, Rank: r})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].XP != rows[j].XP {
			return rows[i].XP > rows[j].XP
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}
