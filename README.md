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
Dockerfile, k8s/     image du jeu et déploiement d'une plateforme partagée
```

Ajouter un défi : écrire une fonction renvoyant un `*game.Challenge` (récit, objectif, étapes, indices, `Check`) et l'ajouter à la liste `Challenges` de son chapitre.

## Configuration

| Variable | Défaut | Rôle |
|---|---|---|
| `CQ_ADDR` (ou `-addr`) | `127.0.0.1:8090` | adresse d'écoute du jeu |
| `CQ_DATA` (ou `-data`) | `data/progress.json` | fichier de progression |
| `CQ_S3_ENDPOINT`, `CQ_DYNAMO_ENDPOINT`, `CQ_SQS_ENDPOINT` | ports ci-dessus | adresses des émulateurs |
| `CQ_ACCESS_KEY`, `CQ_SECRET_KEY`, `CQ_REGION` | `cloudquest`, `cloudquest-secret`, `us-east-1` | identifiants |

## Plateforme partagée (une instance pour toute une classe)

Avec `CQ_MULTI=1`, une seule instance du jeu et des émulateurs sert toute la classe :

- chaque étudiant·e se connecte avec **prénom + nom + code secret** (choisi à la première connexion) ;
- ses ressources portent un **suffixe personnel** (`livrexpress-factures-aminab`, `Commandes-aminab`,
  `commandes-entrantes-aminab`, `paiements-aminab.fifo`…) : les énoncés, les vérifications et les messages
  l'affichent automatiquement, et personne ne profite du travail d'un·e autre ;
- les étudiants utilisent l'AWS CLI depuis leur poste, avec les adresses publiques et la clé affichées dans
  « Accès et identifiants » ;
- pour le projet final, le worker lit le suffixe dans `CQ_SUFFIXE`.

| Variable | Rôle |
|---|---|
| `CQ_MULTI=1` | active les comptes et les suffixes |
| `CQ_S3_PUBLIC`, `CQ_DYNAMO_PUBLIC`, `CQ_SQS_PUBLIC` | adresses montrées aux étudiants (le jeu garde `CQ_*_ENDPOINT` pour ses propres appels) |
| `CQ_STUDENT_ACCESS_KEY`, `CQ_STUDENT_SECRET_KEY` | identifiants montrés aux étudiants (défaut : ceux du jeu) |
| `CQ_S3_CONSOLE`, `CQ_SQS_CONSOLE` | adresse des interfaces web, ou `none` pour masquer le lien |
| `CQ_TEACHER_PASSWORD` | active l'espace formateur |
| `CQ_DOCS` | dossier servi sous `/docs/` (cahier de projet PDF) |

`Dockerfile` construit l'image du jeu ; `k8s/cloudquest.yaml` déploie le jeu, MinIO, DynamoDB Local (tables sur disque)
et ElasticMQ derrière un Ingress Traefik. Le Secret n'est pas dans le dépôt : les commandes sont en tête du manifeste.
Les clés d'accès doivent être **alphanumériques** (DynamoDB Local refuse les autres caractères). DynamoDB Local et
ElasticMQ ne vérifient pas les signatures : filtrez leurs adresses publiques sur la clé d'accès de la classe
(en-tête `Authorization` contenant `Credential=<clé>/`) dans le proxy frontal.

### Espace formateur

`/formateur` (mot de passe `CQ_TEACHER_PASSWORD`, disponible aussi en local) affiche les scores de tous les étudiants :
classement détaillé (XP, défis par chapitre, quiz, indices, essais, dernière activité), taux de réussite par défi,
fiche par étudiant·e, export CSV, actualisation automatique. On peut y réinitialiser un code secret oublié ou supprimer un compte.

## Limites à connaître

- En local, les ressources (buckets, tables, files) ont des noms fixes : **une instance des émulateurs par étudiant·e**. Pour une classe entière sur une seule instance, voir « Plateforme partagée ».
- Sur une plateforme partagée, tous les étudiants utilisent la même clé : le suffixe évite les collisions, il n'empêche pas de toucher aux ressources d'un·e autre.
- DynamoDB Local et ElasticMQ tournent en mémoire : `docker compose down` efface tables et files (les buckets MinIO sont conservés dans un volume). La progression du jeu, elle, reste dans `data/progress.json`.
- Tout repartir de zéro : `docker compose down -v && rm -rf data`.

## Tests

```bash
go test ./...
```

Les tests contrôlent la cohérence du catalogue et exécutent chaque vérification sur un état vierge (émulateurs démarrés requis pour ce dernier test).
