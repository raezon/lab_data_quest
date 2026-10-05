package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // fuseau Europe/Paris disponible même dans une image minimale
	"unicode"

	"cloudquest/internal/game"
)

// Comptes du mode « plateforme partagée » et espace formateur.

const (
	sessionCookie = "cq_session"
	teacherCookie = "cq_teacher"
	minCodeLen    = 4
	maxPlayers    = 500
)

// playerKey renvoie la clé du joueur connecté ("" si personne).
func (s *Server) playerKey(r *http.Request) string {
	if !s.cloud.Cfg.Hosted {
		return playerName(r)
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	key := ""
	s.store.Do(func() (bool, error) {
		if k, ok := s.store.Sessions[c.Value]; ok && s.store.Players[k] != nil {
			key = k
		}
		return false, nil
	})
	return key
}

var accents = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ã", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i", "ô", "o", "ö", "o", "ó", "o", "ù", "u", "û", "u", "ü", "u", "ú", "u",
	"ÿ", "y", "ñ", "n", "œ", "oe", "æ", "ae",
)

// fold normalise un nom pour retrouver un compte : « Élodie  MARTIN » et
// « elodie martin » désignent la même personne.
func fold(name string) string {
	return accents.Replace(strings.ToLower(cleanName(name)))
}

// newID fabrique le suffixe des ressources d'un·e étudiant·e : court,
// lisible, valable dans un nom de bucket, et unique sur l'instance.
func newID(key string, taken map[string]bool) string {
	alnum := func(w string, max int) string {
		var b strings.Builder
		for _, r := range w {
			if r < unicode.MaxASCII && (unicode.IsLower(r) || unicode.IsDigit(r)) && b.Len() < max {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	// Prénom + initiale du nom : « amina benali » donne « aminab ».
	words := strings.Fields(key)
	id := ""
	if len(words) > 0 {
		id = alnum(words[0], 8)
		if len(words) > 1 {
			id += alnum(words[len(words)-1], 1)
		}
	}
	if len(id) < 2 {
		id = "eleve" + id
	}
	// « dlq » transformerait commandes-entrantes en commandes-entrantes-dlq.
	for n, base := 2, id; taken[id] || id == "dlq"; n++ {
		id = base + strconv.Itoa(n)
	}
	return id
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func hashCode(salt, code string) string {
	sum := sha256.Sum256([]byte(salt + ":" + code))
	return hex.EncodeToString(sum[:])
}

func sameString(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// loginHosted connecte un·e étudiant·e avec son nom et son code secret ; le
// compte est créé à la première connexion.
func (s *Server) loginHosted(w http.ResponseWriter, r *http.Request, name string) {
	code := strings.TrimSpace(r.FormValue("code"))
	key := fold(name)
	back := func(msg string) {
		http.Redirect(w, r, "/?erreur="+url.QueryEscape(msg)+"&nom="+url.QueryEscape(name), http.StatusSeeOther)
	}
	if len([]rune(key)) < 3 {
		back("Indiquez votre prénom et votre nom (3 caractères au minimum).")
		return
	}
	if len(code) < minCodeLen || len(code) > 64 {
		back(fmt.Sprintf("Choisissez un code secret d'au moins %d caractères.", minCodeLen))
		return
	}
	token, problem := randomHex(16), ""
	err := s.store.Do(func() (bool, error) {
		p, exists := s.store.Players[key]
		switch {
		case !exists:
			if len(s.store.Players) >= maxPlayers {
				problem = "Cette instance n'accepte plus de nouveaux comptes."
				return false, nil
			}
			taken := map[string]bool{}
			for _, o := range s.store.Players {
				taken[o.ID] = true
			}
			p = s.store.Player(key)
			p.Name, p.ID = name, newID(key, taken)
			fallthrough
		case p.PinHash == "": // nouveau compte, ou code réinitialisé par le formateur
			p.PinSalt = randomHex(8)
			p.PinHash = hashCode(p.PinSalt, code)
		case !sameString(p.PinHash, hashCode(p.PinSalt, code)):
			problem = "Ce nom est déjà utilisé et le code secret ne correspond pas. Code oublié ? Demandez au formateur de le réinitialiser."
			return false, nil
		}
		p.LastSeen = time.Now()
		s.store.Sessions[token] = key
		return true, nil
	})
	if err != nil {
		s.fail(w, err)
		return
	}
	if problem != "" {
		time.Sleep(400 * time.Millisecond) // freine les essais en série
		back(problem)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", MaxAge: 180 * 24 * 3600, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- Espace formateur --------------------------------------------------------

var paris = func() *time.Location {
	if l, err := time.LoadLocation("Europe/Paris"); err == nil {
		return l
	}
	return time.UTC
}()

func when(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(paris).Format("02/01 15:04")
}

func (s *Server) teacherToken() string { return hashCode("cq-formateur", s.teacherPass) }

func (s *Server) isTeacher(r *http.Request) bool {
	c, err := r.Cookie(teacherCookie)
	return s.teacherPass != "" && err == nil && sameString(c.Value, s.teacherToken())
}

// guard répond 404 si l'espace formateur est désactivé, et renvoie vers la
// page de connexion si le formateur n'est pas identifié.
func (s *Server) guard(w http.ResponseWriter, r *http.Request) bool {
	if s.teacherPass == "" {
		http.NotFound(w, r)
		return false
	}
	if !s.isTeacher(r) {
		http.Redirect(w, r, "/formateur", http.StatusSeeOther)
		return false
	}
	return true
}

func (s *Server) teacherLogin(w http.ResponseWriter, r *http.Request) {
	if s.teacherPass == "" {
		http.NotFound(w, r)
		return
	}
	if !sameString(r.FormValue("password"), s.teacherPass) {
		time.Sleep(600 * time.Millisecond)
		http.Redirect(w, r, "/formateur?erreur=1", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: teacherCookie, Value: s.teacherToken(), Path: "/formateur", MaxAge: 30 * 24 * 3600, HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, "/formateur", http.StatusSeeOther)
}

func (s *Server) teacherLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: teacherCookie, Path: "/formateur", MaxAge: -1})
	http.Redirect(w, r, "/formateur", http.StatusSeeOther)
}

type teacherView struct {
	base
	Authed    bool
	BadLogin  bool
	Auto      bool
	Notice    string
	Rows      []game.PlayerScore
	Stats     []game.ChallengeStat
	Count     int
	Active    int // joueurs ayant marqué au moins un point
	Finished  int // joueurs ayant réussi tous les défis
	AvgXP     int
	AvgPct    int
	Generated time.Time
}

func (s *Server) teacher(w http.ResponseWriter, r *http.Request) {
	if s.teacherPass == "" {
		http.NotFound(w, r)
		return
	}
	v := teacherView{Authed: s.isTeacher(r), BadLogin: r.URL.Query().Get("erreur") != "", Auto: r.URL.Query().Get("auto") == "1",
		Notice: r.URL.Query().Get("info"), Generated: time.Now()}
	s.store.Do(func() (bool, error) { v.base = s.base(nil, "Espace formateur"); return false, nil })
	if v.Authed {
		v.Rows, v.Stats = s.store.Scores(s.cat)
		v.Count = len(v.Rows)
		total := 0
		for _, row := range v.Rows {
			total += row.XP
			if row.XP > 0 {
				v.Active++
			}
			if row.Total > 0 && row.Done == row.Total {
				v.Finished++
			}
		}
		if v.Count > 0 {
			v.AvgXP = total / v.Count
			if v.MaxXP > 0 {
				v.AvgPct = v.AvgXP * 100 / v.MaxXP
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, "teacher", v)
}

// teacherCSV exporte le tableau des scores (une ligne par étudiant·e).
func (s *Server) teacherCSV(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	rows, _ := s.store.Scores(s.cat)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="cloudquest-scores-`+time.Now().In(paris).Format("2006-01-02")+`.csv"`)
	w.Write([]byte("\xEF\xBB\xBF")) // BOM : Excel reconnaît l'UTF-8
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	head := []string{"Rang", "Nom", "Identifiant", "XP", "XP max", "%", "Grade", "Défis réussis", "Défis", "Quiz justes", "Quiz répondus", "Questions", "Indices", "Tentatives"}
	for _, ch := range s.cat.Chapters {
		head = append(head, ch.Service+" — défis", ch.Service+" — XP")
	}
	for _, ch := range s.cat.Chapters {
		for _, c := range ch.Challenges {
			head = append(head, c.ID)
		}
	}
	head = append(head, "Inscription", "Dernière activité")
	cw.Write(head)
	for _, row := range rows {
		rec := []string{strconv.Itoa(row.Pos), row.Player.Name, row.Player.ID, strconv.Itoa(row.XP), strconv.Itoa(s.cat.MaxXP()), strconv.Itoa(row.Pct), row.Rank.Name,
			strconv.Itoa(row.Done), strconv.Itoa(row.Total), strconv.Itoa(row.QuizRight), strconv.Itoa(row.QuizDone), strconv.Itoa(row.QuizTotal),
			strconv.Itoa(row.Hints), strconv.Itoa(row.Attempts)}
		for _, cs := range row.Chapters {
			rec = append(rec, fmt.Sprintf("%d/%d", cs.Done, cs.Total), strconv.Itoa(cs.XP))
		}
		for _, d := range row.Challenges {
			if d.State.Done {
				rec = append(rec, strconv.Itoa(d.State.XPEarned))
			} else {
				rec = append(rec, "")
			}
		}
		rec = append(rec, csvTime(row.Player.Created), csvTime(row.Last))
		cw.Write(rec)
	}
	cw.Flush()
}

func csvTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(paris).Format("2006-01-02 15:04")
}

// teacherResetCode efface le code secret d'un compte : l'étudiant·e en
// choisit un nouveau à sa prochaine connexion. Sa progression est conservée.
func (s *Server) teacherResetCode(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	key, name := r.FormValue("cle"), ""
	err := s.store.Do(func() (bool, error) {
		p := s.store.Players[key]
		if p == nil {
			return false, nil
		}
		name, p.PinSalt, p.PinHash = p.Name, "", ""
		s.dropSessions(key)
		return true, nil
	})
	s.teacherDone(w, r, err, name, "Code secret de %s réinitialisé : il sera choisi à la prochaine connexion.")
}

// teacherDelete supprime un compte et sa progression (pas ses ressources
// dans les émulateurs).
func (s *Server) teacherDelete(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	key, name := r.FormValue("cle"), ""
	err := s.store.Do(func() (bool, error) {
		p := s.store.Players[key]
		if p == nil {
			return false, nil
		}
		name = p.Name
		delete(s.store.Players, key)
		s.dropSessions(key)
		return true, nil
	})
	s.teacherDone(w, r, err, name, "Compte de %s supprimé.")
}

// dropSessions déconnecte un joueur (à appeler sous verrou du store).
func (s *Server) dropSessions(key string) {
	for tok, k := range s.store.Sessions {
		if k == key {
			delete(s.store.Sessions, tok)
		}
	}
}

func (s *Server) teacherDone(w http.ResponseWriter, r *http.Request, err error, name, msg string) {
	if err != nil {
		s.fail(w, err)
		return
	}
	info := "Compte introuvable."
	if name != "" {
		info = fmt.Sprintf(msg, name)
	}
	http.Redirect(w, r, "/formateur?info="+url.QueryEscape(info), http.StatusSeeOther)
}
