# ☁️ CloudQuest

Un jeu web pour apprendre les services de données d'AWS **en local, sans compte AWS** :

| Service AWS | Émulateur local | API | Interface web |
|---|---|---|---|
| Amazon S3 | MinIO | `http://127.0.0.1:9100` | `http://127.0.0.1:9101` |
| Amazon DynamoDB | DynamoDB Local | `http://127.0.0.1:8100` | — |
| Amazon SQS | ElasticMQ | `http://127.0.0.1:9324` | `http://127.0.0.1:9325` |

L'étudiant·e incarne un·e ingénieur·e cloud junior chez **LivrExpress** et progresse par défis.
Chaque défi est **vérifié sur l'état réel des émulateurs** (le bucket, la table ou la file doivent vraiment exister).

## Démarrage

Prérequis : Docker (avec Compose), Go ≥ 1.24 et `make`.

```bash
make install                  # vérifie les prérequis, télécharge modules Go et images Docker
make start                    # émulateurs + jeu : http://127.0.0.1:8090
```

`make` seul liste toutes les commandes (`up`, `down`, `test`, `s3`, `dynamo`, `sqs`, `reset`…).
Par exemple : `make s3 ARGS="ls"`, `make dynamo ARGS="list-tables"`, `make sqs ARGS="list-queues"`.

Sans `make`, l'équivalent à la main :

```bash
docker compose up -d          # MinIO, DynamoDB Local, ElasticMQ
go run ./cmd/cloudquest       # le jeu : http://127.0.0.1:8090
```

Identifiants locaux : clé d'accès `cloudquest`, clé secrète `cloudquest-secret`, région `us-east-1`.

Deux clients en ligne de commande sont fournis, déjà configurés :

```bash
docker compose run --rm mc ls local
docker compose run --rm aws s3 ls --endpoint-url http://127.0.0.1:9100
docker compose run --rm aws dynamodb list-tables --endpoint-url http://127.0.0.1:8100
docker compose run --rm aws sqs list-queues --endpoint-url http://127.0.0.1:9324
```

## Contenu

| Chapitre | Défis | Thèmes |
|---|---|---|
| 1. L'entrepôt infini (S3) | 8 | bucket, objets, préfixes, métadonnées, versioning, cycle de vie, URL pré-signée, politique |
| 2. Le registre éclair (DynamoDB) | 6 | table, items, clé composée, GSI, Query vs Scan, TTL |
| 3. Le tapis roulant (SQS) | 5 | file, envoi/réception, délai de visibilité, DLQ, FIFO |
| 4. Projet final | 2 | worker Go SQS → DynamoDB + S3, puis résistance aux messages toxiques |

Chaque chapitre se termine par un quiz (une tentative par question).

### Règles du jeu

- Les défis d'un chapitre se débloquent dans l'ordre ; le projet final s'ouvre quand les chapitres 1 à 3 sont terminés.
- Chaque défi propose un **parcours guidé** : des étapes expliquées et des **indices progressifs** qui orientent sans jamais donner la commande complète.
- Un indice révélé coûte 10 % des points du défi (plancher : 40 %). Les vérifications sont illimitées et gratuites.
- Un classement (`/classement`) compare les joueurs enregistrés sur la même instance.

### Projet et cahier PDF

- `projet/pipeline/main.go` — squelette du worker à compléter (`go run ./projet/pipeline`). Il compile tel quel ; les fonctions à écrire sont marquées `TODO`.
- `docs/cahier-de-projet.pdf` — cahier de projet élève en français : contexte, problème à résoudre, parcours guidé par phase, 20 questions d'analyse, livrables et barème.
  Pour le régénérer après modification de `docs/cahier-de-projet.html` : `weasyprint docs/cahier-de-projet.html docs/cahier-de-projet.pdf`.

## Organisation du code

```
cmd/cloudquest/      point d'entrée du serveur web
internal/cloud/      clients AWS SDK v2 pointés vers les émulateurs
internal/game/       chapitres, défis, indices, quiz, score, progression (ch_*.go = contenu)
internal/web/        routes HTTP, gabarits HTML et CSS embarqués
projet/pipeline/     squelette élève du projet final
docs/                cahier de projet (HTML source + PDF)
docker-compose.yml   émulateurs + clients mc et aws (profil « tools »)
```

Ajouter un défi : écrire une fonction renvoyant un `*game.Challenge` (récit, objectif, étapes, indices, `Check`) et l'ajouter à la liste `Challenges` de son chapitre.

## Configuration

| Variable | Défaut | Rôle |
|---|---|---|
| `CQ_ADDR` (ou `-addr`) | `127.0.0.1:8090` | adresse d'écoute du jeu |
| `CQ_DATA` (ou `-data`) | `data/progress.json` | fichier de progression |
| `CQ_S3_ENDPOINT`, `CQ_DYNAMO_ENDPOINT`, `CQ_SQS_ENDPOINT` | ports ci-dessus | adresses des émulateurs |
| `CQ_ACCESS_KEY`, `CQ_SECRET_KEY`, `CQ_REGION` | `cloudquest`, `cloudquest-secret`, `us-east-1` | identifiants |

## Limites à connaître

- Les ressources (buckets, tables, files) ont des noms fixes : **une instance des émulateurs par étudiant·e**. Le plus simple est que chacun lance le projet sur son poste.
- DynamoDB Local et ElasticMQ tournent en mémoire : `docker compose down` efface tables et files (les buckets MinIO sont conservés dans un volume). La progression du jeu, elle, reste dans `data/progress.json`.
- Tout repartir de zéro : `docker compose down -v && rm -rf data`.

## Tests

```bash
go test ./...
```

Les tests contrôlent la cohérence du catalogue et exécutent chaque vérification sur un état vierge (émulateurs démarrés requis pour ce dernier test).
