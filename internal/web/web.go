// Package web expose l'interface du jeu : pages HTML rendues côté serveur,
// formulaires classiques, aucune dépendance JavaScript externe.
package web

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cloudquest/internal/cloud"
	"cloudquest/internal/game"
)

//go:embed templates/*.html
var tplFS embed.FS

//go:embed static
var staticFS embed.FS

const cookieName = "cq_player"

// Server relie le catalogue, la progression et les clients cloud.
type Server struct {
	cat   *game.Catalog
	store *game.Store
	cloud *cloud.Clients
	tpl   map[string]*template.Template
}

// New prépare les gabarits et renvoie le serveur.
func New(cat *game.Catalog, store *game.Store, c *cloud.Clients) (*Server, error) {
	funcs := template.FuncMap{
		"raw": func(s string) template.HTML { return template.HTML(s) },
		"inc": func(i int) int { return i + 1 },
	}
	s := &Server{cat: cat, store: store, cloud: c, tpl: map[string]*template.Template{}}
	for _, page := range []string{"login", "home", "chapter", "challenge", "leaderboard"} {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(tplFS, "templates/layout.html", "templates/"+page+".html")
		if err != nil {
			return nil, err
		}
		s.tpl[page] = t
	}
	return s, nil
}

// Handler renvoie le routeur HTTP.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /chapitre/{id}", s.chapter)
	mux.HandleFunc("GET /defi/{id}", s.challenge)
	mux.HandleFunc("POST /defi/{id}/indice", s.hint)
	mux.HandleFunc("POST /defi/{id}/preparer", s.setup)
	mux.HandleFunc("POST /defi/{id}/verifier", s.check)
	mux.HandleFunc("POST /quiz/{id}", s.quiz)
	mux.HandleFunc("GET /classement", s.leaderboard)
	return mux
}

// --- session ---------------------------------------------------------------

func playerName(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	n, _ := url.QueryUnescape(c.Value)
	return cleanName(n)
}

func cleanName(n string) string {
	n = strings.Join(strings.Fields(n), " ")
	if r := []rune(n); len(r) > 30 {
		n = string(r[:30])
	}
	return n
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	name := cleanName(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := s.store.Do(func() (bool, error) { s.store.Player(name); return true, nil }); err != nil {
		s.fail(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: url.QueryEscape(name), Path: "/", MaxAge: 180 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- vues ------------------------------------------------------------------

type base struct {
	Title    string
	Player   string
	XP       int
	MaxXP    int
	Pct      int
	Rank     game.Rank
	Next     *game.Rank
	ToNext   int
	Health   map[string]bool
	AllUp    bool
	Cfg      cloud.Config
	Chapters []*game.Chapter
}

// base construit l'en-tête commun. À appeler sous verrou du store.
func (s *Server) base(p *game.Player, title string) base {
	b := base{Title: title, MaxXP: s.cat.MaxXP(), Cfg: s.cloud.Cfg, Chapters: s.cat.Chapters}
	if p != nil {
		b.Player, b.XP = p.Name, p.XP()
		b.Rank, b.Next = game.RankFor(b.XP)
		if b.Next != nil {
			b.ToNext = b.Next.MinXP - b.XP
		}
		if b.MaxXP > 0 {
			b.Pct = b.XP * 100 / b.MaxXP
		}
	}
	return b
}

func (b *base) withHealth(c *cloud.Clients) {
	b.Health = c.Health()
	b.AllUp = b.Health["s3"] && b.Health["dynamodb"] && b.Health["sqs"]
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	var buf bytes.Buffer
	if err := s.tpl[page].Execute(&buf, data); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Printf("erreur: %v", err)
	http.Error(w, "Erreur interne : "+err.Error(), http.StatusInternalServerError)
}

// chapterLocked indique si les prérequis d'un chapitre manquent.
func (s *Server) chapterLocked(p *game.Player, ch *game.Chapter) bool {
	for _, id := range ch.RequiresAll {
		if dep := s.cat.Chapter(id); dep != nil && !p.ChapterDone(dep) {
			return true
		}
	}
	return false
}

// challengeLocked : les défis d'un chapitre se débloquent dans l'ordre.
func (s *Server) challengeLocked(p *game.Player, c *game.Challenge) bool {
	if s.chapterLocked(p, c.Chapter) {
		return true
	}
	return c.Index > 0 && !p.IsDone(c.Chapter.Challenges[c.Index-1].ID)
}

type chapterCard struct {
	*game.Chapter
	Locked    bool
	Done      int
	Total     int
	Pct       int
	QuizDone  int
	QuizTotal int
	Complete  bool
	Requires  []string
}

func (s *Server) card(p *game.Player, ch *game.Chapter) chapterCard {
	c := chapterCard{Chapter: ch, Locked: s.chapterLocked(p, ch), Total: len(ch.Challenges), QuizTotal: len(ch.Quiz)}
	for _, x := range ch.Challenges {
		if p.IsDone(x.ID) {
			c.Done++
		}
	}
	for _, q := range ch.Quiz {
		if _, ok := p.Quiz[q.ID]; ok {
			c.QuizDone++
		}
	}
	if c.Total > 0 {
		c.Pct = c.Done * 100 / c.Total
	}
	c.Complete = c.Done == c.Total
	for _, id := range ch.RequiresAll {
		if dep := s.cat.Chapter(id); dep != nil {
			c.Requires = append(c.Requires, dep.Service)
		}
	}
	return c
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	name := playerName(r)
	if name == "" {
		b := s.base(nil, "Bienvenue")
		b.withHealth(s.cloud)
		s.render(w, "login", struct {
			base
			Ranks []game.Rank
		}{b, game.Ranks})
		return
	}
	var data struct {
		base
		Cards []chapterCard
		Ranks []game.Rank
	}
	s.store.Do(func() (bool, error) {
		p := s.store.Player(name)
		data.base = s.base(p, "Tableau de bord")
		for _, ch := range s.cat.Chapters {
			data.Cards = append(data.Cards, s.card(p, ch))
		}
		return false, nil
	})
	data.Ranks = game.Ranks
	data.withHealth(s.cloud)
	s.render(w, "home", data)
}

type challengeRow struct {
	*game.Challenge
	Done   bool
	Locked bool
	Earned int
}

type quizRow struct {
	game.Question
	Num      int
	Answered bool
	Choice   int
	Correct  bool
}

func (s *Server) chapter(w http.ResponseWriter, r *http.Request) {
	name := playerName(r)
	ch := s.cat.Chapter(r.PathValue("id"))
	if name == "" || ch == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	var data struct {
		base
		Card       chapterCard
		Challenges []challengeRow
		Quiz       []quizRow
	}
	s.store.Do(func() (bool, error) {
		p := s.store.Player(name)
		data.base = s.base(p, ch.Title)
		data.Card = s.card(p, ch)
		for _, c := range ch.Challenges {
			row := challengeRow{Challenge: c, Done: p.IsDone(c.ID), Locked: s.challengeLocked(p, c)}
			if st, ok := p.Challenges[c.ID]; ok {
				row.Earned = st.XPEarned
			}
			data.Challenges = append(data.Challenges, row)
		}
		for i, q := range ch.Quiz {
			row := quizRow{Question: q, Num: i + 1}
			if qs, ok := p.Quiz[q.ID]; ok {
				row.Answered, row.Choice, row.Correct = true, qs.Choice, qs.Correct
			}
			data.Quiz = append(data.Quiz, row)
		}
		return false, nil
	})
	s.render(w, "chapter", data)
}

type hintView struct {
	Index     int
	Text      string
	Revealed  bool
	CanReveal bool
}

type stepView struct {
	Index int
	game.Step
	Hints []hintView
}

type challengeView struct {
	base
	C         *game.Challenge
	Chapter   *game.Chapter
	Num       int
	Count     int
	State     game.ChallengeState
	Steps     []stepView
	HintsUsed int
	Potential int
	Penalty   int
	MinPct    int
	Items     []game.CheckItem
	Checked   bool
	Success   bool
	Gained    int
	Answer    string
	SetupErr  string
	NextC     *game.Challenge
}

func (s *Server) buildChallenge(p *game.Player, c *game.Challenge) challengeView {
	st := p.State(c.ID)
	v := challengeView{
		base: s.base(p, c.Title), C: c, Chapter: c.Chapter, Num: c.Index + 1, Count: len(c.Chapter.Challenges),
		State: *st, HintsUsed: st.HintCount(), Penalty: game.HintPenaltyPct, MinPct: game.MinXPPct,
	}
	v.Potential = game.EarnedXP(c, v.HintsUsed)
	for i, step := range c.Steps {
		sv := stepView{Index: i, Step: step}
		next := true // le premier indice non révélé de l'étape est le seul révélable
		for j, h := range step.Hints {
			hv := hintView{Index: j, Text: h, Revealed: st.HintsUsed[hintKey(i, j)]}
			if !hv.Revealed && next {
				hv.CanReveal, next = true, false
			}
			sv.Hints = append(sv.Hints, hv)
		}
		v.Steps = append(v.Steps, sv)
	}
	if c.Index+1 < len(c.Chapter.Challenges) {
		v.NextC = c.Chapter.Challenges[c.Index+1]
	}
	return v
}

func hintKey(step, hint int) string { return fmt.Sprintf("%d:%d", step, hint) }

// load renvoie le joueur et le défi de la requête, ou redirige.
func (s *Server) load(w http.ResponseWriter, r *http.Request) (string, *game.Challenge, bool) {
	name := playerName(r)
	c := s.cat.Challenge(r.PathValue("id"))
	if name == "" || c == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return "", nil, false
	}
	return name, c, true
}

func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	name, c, ok := s.load(w, r)
	if !ok {
		return
	}
	var v challengeView
	locked := false
	s.store.Do(func() (bool, error) {
		p := s.store.Player(name)
		if locked = s.challengeLocked(p, c); !locked {
			v = s.buildChallenge(p, c)
		}
		return false, nil
	})
	if locked {
		http.Redirect(w, r, "/chapitre/"+c.Chapter.ID, http.StatusSeeOther)
		return
	}
	v.SetupErr = r.URL.Query().Get("erreur")
	s.render(w, "challenge", v)
}

func (s *Server) hint(w http.ResponseWriter, r *http.Request) {
	name, c, ok := s.load(w, r)
	if !ok {
		return
	}
	step, _ := strconv.Atoi(r.FormValue("etape"))
	idx, _ := strconv.Atoi(r.FormValue("indice"))
	err := s.store.Do(func() (bool, error) {
		p := s.store.Player(name)
		if s.challengeLocked(p, c) || step < 0 || step >= len(c.Steps) || idx < 0 || idx >= len(c.Steps[step].Hints) {
			return false, nil
		}
		st := p.State(c.ID)
		// Les indices se révèlent dans l'ordre, du plus vague au plus précis.
		if idx > 0 && !st.HintsUsed[hintKey(step, idx-1)] {
			return false, nil
		}
		st.HintsUsed[hintKey(step, idx)] = true
		return true, nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/defi/%s#etape-%d", c.ID, step+1), http.StatusSeeOther)
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	name, c, ok := s.load(w, r)
	if !ok || c.Setup == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	var runErr error
	err := s.store.Do(func() (bool, error) {
		p := s.store.Player(name)
		if s.challengeLocked(p, c) {
			return false, nil
		}
		st := p.State(c.ID)
		var msg string
		if msg, runErr = c.Setup.Run(ctx, s.cloud, st); runErr != nil {
			return false, nil
		}
		st.SetupInfo = msg
		return true, nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	target := "/defi/" + c.ID
	if runErr != nil {
		target += "?erreur=" + url.QueryEscape(runErr.Error())
	}
	http.Redirect(w, r, target+"#mission", http.StatusSeeOther)
}

func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	name, c, ok := s.load(w, r)
	if !ok {
		return
	}
	answer := strings.TrimSpace(r.FormValue("reponse"))
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	var v challengeView
	locked := false
	err := s.store.Do(func() (bool, error) {
		p := s.store.Player(name)
		if locked = s.challengeLocked(p, c); locked {
			return false, nil
		}
		st := p.State(c.ID)
		st.Attempts++
		items := runCheck(ctx, s.cloud, c, answer, st)
		success := len(items) > 0
		for _, it := range items {
			success = success && it.OK
		}
		gained := 0
		if success && !st.Done {
			st.Done, st.DoneAt = true, time.Now()
			st.XPEarned = game.EarnedXP(c, st.HintCount())
			gained = st.XPEarned
		}
		v = s.buildChallenge(p, c)
		v.Items, v.Checked, v.Success, v.Gained, v.Answer = items, true, success, gained, answer
		return true, nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	if locked {
		http.Redirect(w, r, "/chapitre/"+c.Chapter.ID, http.StatusSeeOther)
		return
	}
	s.render(w, "challenge", v)
}

// runCheck exécute la vérification en protégeant le serveur d'une panique.
func runCheck(ctx context.Context, cl *cloud.Clients, c *game.Challenge, answer string, st *game.ChallengeState) (items []game.CheckItem) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("panique dans la vérification de %s: %v", c.ID, rec)
			items = []game.CheckItem{{Label: "Vérification", Detail: "erreur interne pendant la vérification — les émulateurs sont-ils démarrés ?"}}
		}
	}()
	return c.Check(ctx, cl, answer, st)
}

func (s *Server) quiz(w http.ResponseWriter, r *http.Request) {
	name := playerName(r)
	q := s.cat.Question(r.PathValue("id"))
	choice, convErr := strconv.Atoi(r.FormValue("choix"))
	if name == "" || q == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	err := s.store.Do(func() (bool, error) {
		p := s.store.Player(name)
		// Une seule tentative par question : les points récompensent le premier essai.
		if _, done := p.Quiz[q.ID]; done || convErr != nil || choice < 0 || choice >= len(q.Options) {
			return false, nil
		}
		p.Quiz[q.ID] = &game.QuizState{Choice: choice, Correct: choice == q.Answer}
		return true, nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	back := "/"
	for _, ch := range s.cat.Chapters {
		for _, x := range ch.Quiz {
			if x.ID == q.ID {
				back = "/chapitre/" + ch.ID + "#" + q.ID
			}
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) leaderboard(w http.ResponseWriter, r *http.Request) {
	name := playerName(r)
	var b base
	s.store.Do(func() (bool, error) {
		if name != "" {
			b = s.base(s.store.Player(name), "Classement")
		} else {
			b = s.base(nil, "Classement")
		}
		return false, nil
	})
	s.render(w, "leaderboard", struct {
		base
		Rows []game.LeaderRow
	}{b, s.store.Leaderboard()})
}
