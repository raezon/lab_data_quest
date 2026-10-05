// Commande cloudquest : lance le serveur web du jeu.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"cloudquest/internal/cloud"
	"cloudquest/internal/game"
	"cloudquest/internal/web"
)

func main() {
	addr := flag.String("addr", envOr("CQ_ADDR", "127.0.0.1:8090"), "adresse d'écoute du serveur web")
	data := flag.String("data", envOr("CQ_DATA", "data/progress.json"), "fichier de progression des joueurs")
	flag.Parse()

	clients, err := cloud.New(context.Background(), cloud.ConfigFromEnv())
	if err != nil {
		log.Fatalf("configuration des clients cloud : %v", err)
	}
	store, err := game.OpenStore(*data)
	if err != nil {
		log.Fatalf("ouverture de %s : %v", *data, err)
	}
	cat := game.NewCatalog()
	if clients.Cfg.Hosted {
		cat.ApplyHosted()
		log.Print("mode plateforme partagée : comptes avec code secret, ressources suffixées par étudiant·e")
	}
	teacher := os.Getenv("CQ_TEACHER_PASSWORD")
	if teacher != "" {
		log.Print("espace formateur activé : /formateur")
	}
	srv, err := web.New(cat, store, clients, teacher)
	if err != nil {
		log.Fatalf("chargement des gabarits : %v", err)
	}

	for name, up := range clients.Health() {
		if !up {
			log.Printf("⚠ émulateur %s injoignable — lancez « docker compose up -d »", name)
		}
	}
	log.Printf("CloudQuest est prêt : http://%s", *addr)
	server := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(server.ListenAndServe())
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
