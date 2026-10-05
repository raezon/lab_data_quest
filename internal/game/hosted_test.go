package game

import (
	"strings"
	"testing"

	"cloudquest/internal/cloud"
)

func TestPersonalize(t *testing.T) {
	local := &cloud.Clients{}
	page := `<code>Commandes</code> Commandes du jour, table Commandes existe, <code>livrexpress-factures</code>, commandes-entrantes-dlq, commandes-entrantes, paiements.fifo http://127.0.0.1:9100/x`
	if got := Personalize(page, local); got != page {
		t.Errorf("en local la page ne doit pas changer : %s", got)
	}
	c := (&cloud.Clients{Cfg: cloud.Config{Hosted: true, S3Public: "https://s3.exemple", DynamoPublic: "https://d.exemple", SQSPublic: "https://q.exemple"}}).For("amina")
	want := `<code>Commandes-amina</code> Commandes du jour, table Commandes-amina existe, <code>livrexpress-factures-amina</code>, commandes-entrantes-dlq-amina, commandes-entrantes-amina, paiements-amina.fifo https://s3.exemple/x`
	got := Personalize(page, c)
	if got != want {
		t.Errorf("Personalize :\n obtenu  %s\n attendu %s", got, want)
	}
	// Une page déjà personnalisée (réponse saisie, message d'API) reste stable.
	if again := Personalize(got, c); again != want {
		t.Errorf("le suffixe a été doublé : %s", again)
	}
}

func TestHostedCatalogHidesLocalSetup(t *testing.T) {
	cat := NewCatalog()
	cat.ApplyHosted()
	for _, id := range []string{"s3-bucket", "dyn-table", "sqs-queue"} {
		s := cat.Challenge(id).Steps[0]
		if txt := s.Explain + strings.Join(s.Hints, " "); strings.Contains(txt, "docker") || strings.Contains(txt, "127.0.0.1") {
			t.Errorf("défi %s : la première étape parle encore de l'installation locale", id)
		}
	}
}
