package game

import (
	"context"
	"testing"
	"time"

	"cloudquest/internal/cloud"
)

func TestCatalogIsConsistent(t *testing.T) {
	cat := NewCatalog()
	for _, ch := range cat.Chapters {
		for _, id := range ch.RequiresAll {
			if cat.Chapter(id) == nil {
				t.Errorf("chapitre %s : prérequis inconnu %q", ch.ID, id)
			}
		}
		for _, c := range ch.Challenges {
			if c.Check == nil || len(c.Steps) == 0 || c.TotalHints() == 0 {
				t.Errorf("défi %s incomplet (vérification, étapes ou indices manquants)", c.ID)
			}
		}
		for _, q := range ch.Quiz {
			if q.Answer < 0 || q.Answer >= len(q.Options) {
				t.Errorf("question %s : réponse hors limites", q.ID)
			}
		}
	}
	if last := Ranks[len(Ranks)-1]; last.MinXP > cat.MaxXP() {
		t.Errorf("le dernier grade (%d XP) est inatteignable (max %d XP)", last.MinXP, cat.MaxXP())
	}
}

func TestEarnedXP(t *testing.T) {
	c := &Challenge{XP: 200}
	for hints, want := range map[int]int{0: 200, 1: 180, 6: 80, 20: 80} {
		if got := EarnedXP(c, hints); got != want {
			t.Errorf("EarnedXP(%d indices) = %d, attendu %d", hints, got, want)
		}
	}
}

// Chaque vérification doit échouer proprement (sans panique) sur un état vierge.
func TestChecksOnEmptyState(t *testing.T) {
	cl, err := cloud.New(context.Background(), cloud.ConfigFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	for name, up := range cl.Health() {
		if !up {
			t.Skipf("émulateur %s arrêté (docker compose up -d)", name)
		}
	}
	for _, ch := range NewCatalog().Chapters {
		for _, c := range ch.Challenges {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			st := &ChallengeState{HintsUsed: map[string]bool{}, Secret: map[string]string{}}
			items := c.Check(ctx, cl, "", st)
			cancel()
			if len(items) == 0 {
				t.Errorf("défi %s : aucun point de contrôle renvoyé", c.ID)
			}
		}
	}
}
