package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"cloudquest/internal/cloud"
)

const (
	bucketFactures = "livrexpress-factures"
	bucketArchives = "livrexpress-archives"
	bucketSite     = "livrexpress-site"
	keyFacture1    = "factures/2026/10/F-0001.json"
	keyFacture2    = "factures/2026/10/F-0002.json"
	keyContrat     = "contrats/contrat-transport.txt"
)

func s3Chapter() *Chapter {
	return &Chapter{
		ID:        "s3",
		Title:     "Chapitre 1 — L'entrepôt infini",
		Service:   "Amazon S3",
		LocalTool: "MinIO",
		Icon:      "🪣",
		Color:     "#ff9900",
		Intro: `<p>LivrExpress stocke aujourd'hui ses factures et bons de livraison sur un vieux
partage réseau qui sature chaque mois. La direction veut passer au <strong>stockage objet</strong>,
le modèle d'Amazon S3. Pour apprendre sans facture AWS, vous utiliserez <strong>MinIO</strong>,
un serveur 100&nbsp;% compatible avec l'API S3.</p>
<p>Notions clés : <em>bucket</em>, <em>objet</em>, <em>clé</em>, <em>préfixe</em>, métadonnées,
versioning, cycle de vie, URL pré-signée, politique de bucket.</p>`,
		Challenges: []*Challenge{
			s3CreateBucket(), s3FirstUpload(), s3Prefixes(), s3Metadata(),
			s3Versioning(), s3Lifecycle(), s3Presigned(), s3Policy(),
		},
		Quiz: []Question{
			{ID: "s3q1", Text: "Dans S3, que représente réellement le « dossier » <code>factures/2026/</code> ?",
				Options: []string{"Un répertoire physique sur le disque", "Un simple préfixe dans la clé des objets", "Un bucket enfant", "Un volume EBS monté"},
				Answer:  1, Explain: "S3 est un espace de noms plat : <code>factures/2026/F-1.json</code> est une seule clé. Les consoles affichent des « dossiers » en découpant sur le délimiteur <code>/</code>."},
			{ID: "s3q2", Text: "Pourquoi le nom d'un bucket doit-il être unique sur AWS ?",
				Options: []string{"Parce qu'il fait partie d'un nom DNS global", "Pour des raisons de facturation", "Ce n'est pas le cas", "Parce qu'il sert de clé de chiffrement"},
				Answer:  0, Explain: "Le nom du bucket apparaît dans l'URL (<code>bucket.s3.amazonaws.com</code>) : il est donc unique dans tout l'espace de noms S3 d'une partition AWS."},
			{ID: "s3q3", Text: "Le versioning est activé. Vous supprimez un objet sans préciser de version. Que se passe-t-il ?",
				Options: []string{"L'objet et toutes ses versions sont effacés", "Un « marqueur de suppression » devient la version courante", "La suppression est refusée", "Seule la plus ancienne version est effacée"},
				Answer:  1, Explain: "S3 ajoute un <em>delete marker</em>. Les anciennes versions restent récupérables tant qu'on ne les supprime pas explicitement par leur <code>versionId</code>."},
			{ID: "s3q4", Text: "Quel est l'intérêt principal d'une URL pré-signée ?",
				Options: []string{"Rendre un bucket public", "Accélérer les téléchargements", "Donner un accès temporaire à un objet précis sans partager ses identifiants", "Chiffrer l'objet"},
				Answer:  2, Explain: "L'URL embarque une signature calculée avec vos clés et une date d'expiration. Quiconque la possède peut faire <em>cette</em> opération, sur <em>cet</em> objet, jusqu'à l'expiration."},
			{ID: "s3q5", Text: "Une règle de cycle de vie « Expiration 30 jours » sur le préfixe <code>logs/</code> :",
				Options: []string{"Archive les logs dans Glacier", "Supprime automatiquement les objets de <code>logs/</code> 30 jours après leur création", "Empêche toute écriture après 30 jours", "Déplace les objets dans un autre bucket"},
				Answer:  1, Explain: "L'action <em>Expiration</em> supprime les objets ; l'action <em>Transition</em> les change de classe de stockage (ex. Glacier). MinIO gère l'expiration et des transitions vers des « tiers »."},
			{ID: "s3q6", Text: "Quelle est la taille maximale d'un objet unique dans S3 ?",
				Options: []string{"5 Go", "5 To", "100 Mo", "Illimitée"},
				Answer:  1, Explain: "5 To par objet. Au-delà de 5 Go en une seule requête PUT, il faut l'<em>upload multipart</em> (recommandé dès ~100 Mo)."},
		},
	}
}

// --- outils communs --------------------------------------------------------

func bucketExists(ctx context.Context, c *cloud.Clients, name string) error {
	_, err := c.S3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(name)})
	return err
}

func getObject(ctx context.Context, c *cloud.Clients, bucket, key string) ([]byte, *s3.GetObjectOutput, error) {
	out, err := c.S3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, nil, err
	}
	defer out.Body.Close()
	b, err := io.ReadAll(io.LimitReader(out.Body, 1<<20))
	return b, out, err
}

func countPrefix(ctx context.Context, c *cloud.Clients, bucket, prefix string) (int, error) {
	n := 0
	p := s3.NewListObjectsV2Paginator(c.S3, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), Prefix: aws.String(prefix)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return 0, err
		}
		for _, o := range page.Contents {
			if !strings.HasSuffix(aws.ToString(o.Key), "/") {
				n++
			}
		}
	}
	return n, nil
}

func anonGet(c *cloud.Clients, rawURL string) (int, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	// Une URL publique est rejouée sur l'adresse interne de MinIO en gardant
	// l'en-tête Host d'origine : la signature V4 en dépend.
	if pub, _ := url.Parse(c.Cfg.S3Public); pub != nil && req.URL.Host == pub.Host && c.Cfg.S3Public != c.Cfg.S3Endpoint {
		if in, _ := url.Parse(c.Cfg.S3Endpoint); in != nil {
			req.Host = req.URL.Host
			req.URL.Scheme, req.URL.Host = in.Scheme, in.Host
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode, nil
}

var stepConnectMinio = Step{
	Title: "Choisir son outil et se connecter",
	Explain: `<p>Trois façons de parler à MinIO — choisissez celle que vous voulez, toutes sont valables :</p>
<ul><li>la <strong>console web</strong> de MinIO ;</li>
<li>le client <strong>mc</strong> (MinIO Client) ;</li>
<li>l'<strong>AWS CLI</strong> officielle (c'est elle que vous utiliserez sur le vrai AWS).</li></ul>
<p>Le projet fournit <code>mc</code> et <code>aws</code> déjà configurés via Docker Compose (profil <code>tools</code>).</p>`,
	Hints: []string{
		"Regardez le fichier <code>docker-compose.yml</code> : les ports exposés et les variables <code>MINIO_ROOT_*</code> vous disent tout.",
		"La console web écoute sur le port <strong>9101</strong> ; l'API S3 sur le port <strong>9100</strong>.",
		"Avec l'AWS CLI, chaque commande a besoin de l'option <code>--endpoint-url</code>. Sans elle, la CLI essaie de joindre le vrai AWS !",
	},
}

// --- Défi 1 ----------------------------------------------------------------

func s3CreateBucket() *Challenge {
	return &Challenge{
		ID: "s3-bucket", Title: "Le premier entrepôt", XP: 100,
		Story: `<p>Lundi, 8h. Karim, le directeur technique de LivrExpress, vous accueille :
« Notre serveur de fichiers est plein. On passe au stockage objet. Commence par créer
l'espace qui accueillera toutes nos factures. »</p>`,
		Objective: `Créer un bucket nommé <code>livrexpress-factures</code>.`,
		Concepts:  []string{"Bucket", "Endpoint", "Identifiants d'accès"},
		Docs: []Link{
			{"AWS — Créer un bucket", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/create-bucket-overview.html"},
			{"MinIO — mc mb", "https://min.io/docs/minio/linux/reference/minio-mc/mc-mb.html"},
		},
		Steps: []Step{
			stepConnectMinio,
			{
				Title: "Créer le bucket",
				Explain: `<p>Un <strong>bucket</strong> est le conteneur de premier niveau. Tout objet vit dans un bucket.
Son nom suit des règles strictes car, sur AWS, il devient un nom de domaine.</p>`,
				Hints: []string{
					"Règles de nommage : 3 à 63 caractères, minuscules, chiffres, points et tirets uniquement.",
					"Avec la commande de haut niveau <code>aws s3</code>, cherchez la sous-commande dont l'abréviation évoque <em>make bucket</em>.",
					"Avec <code>mc</code>, la commande ressemble à la commande Unix qui crée un répertoire ; la cible s'écrit <code>alias/nom-du-bucket</code>.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			err := bucketExists(ctx, c, c.N(bucketFactures))
			r.ok("Le bucket livrexpress-factures existe", err == nil, errDetail(err))
			return r.items
		},
	}
}

// --- Défi 2 ----------------------------------------------------------------

func s3FirstUpload() *Challenge {
	return &Challenge{
		ID: "s3-upload", Title: "La première facture", XP: 100,
		Story: `<p>Le service comptable vous transmet la facture n°0001 du client
« Pharmacie El Amel ». Elle doit être stockée au format JSON pour être lue plus tard
par les outils d'analyse.</p>`,
		Objective: `Déposer l'objet <code>factures/2026/10/F-0001.json</code> dans <code>livrexpress-factures</code>.
Son contenu doit être un JSON valide contenant au moins les champs <code>numero</code> (texte),
<code>client</code> (texte) et <code>montant</code> (nombre strictement positif).`,
		Concepts: []string{"Objet", "Clé", "PutObject"},
		Docs:     []Link{{"AWS — Charger des objets", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/upload-objects.html"}},
		Steps: []Step{
			{
				Title:   "Préparer le fichier",
				Explain: `<p>Créez localement un fichier JSON. Validez sa syntaxe avant de l'envoyer : une virgule en trop suffit à le rendre illisible.</p>`,
				Hints: []string{
					"Un JSON est un objet entre accolades, avec des paires <code>\"clé\": valeur</code> séparées par des virgules.",
					"Un nombre JSON ne s'écrit pas entre guillemets : <code>\"12500\"</code> est un texte, pas un nombre.",
					"Pour valider : <code>python3 -m json.tool fichier.json</code> ou <code>jq . fichier.json</code>.",
				},
			},
			{
				Title:   "Envoyer l'objet avec la bonne clé",
				Explain: `<p>Dans S3, la <strong>clé</strong> est le nom complet de l'objet, « dossiers » compris. Le nom de votre fichier local n'a aucune importance : c'est la clé de destination qui compte.</p>`,
				Hints: []string{
					"La clé attendue commence par <code>factures/</code> et se termine par <code>F-0001.json</code>.",
					"<code>aws s3</code> propose une sous-commande qui ressemble à la commande Unix de copie ; la destination s'écrit <code>s3://bucket/clé</code>.",
					"La commande bas niveau <code>aws s3api put-object</code> prend <code>--bucket</code>, <code>--key</code> et <code>--body</code>.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			b, _, err := getObject(ctx, c, c.N(bucketFactures), keyFacture1)
			if !r.ok("L'objet "+keyFacture1+" existe", err == nil, errDetail(err)) {
				return r.pending("Contenu JSON valide", "Champs numero, client, montant")
			}
			var doc map[string]any
			if !r.ok("Contenu JSON valide", json.Unmarshal(b, &doc) == nil, "le contenu n'est pas un objet JSON valide") {
				return r.pending("Champs numero, client, montant")
			}
			_, okN := doc["numero"].(string)
			_, okC := doc["client"].(string)
			m, okM := doc["montant"].(float64)
			var miss []string
			if !okN {
				miss = append(miss, "numero (texte)")
			}
			if !okC {
				miss = append(miss, "client (texte)")
			}
			if !okM || m <= 0 {
				miss = append(miss, "montant (nombre > 0)")
			}
			r.ok("Champs numero, client, montant", len(miss) == 0, "manquant ou mal typé : %s", strings.Join(miss, ", "))
			return r.items
		},
	}
}

// --- Défi 3 ----------------------------------------------------------------

func s3Prefixes() *Challenge {
	return &Challenge{
		ID: "s3-prefixes", Title: "Ranger sans dossiers", XP: 120,
		Story: `<p>Les livreurs scannent leurs bons de livraison depuis deux entrepôts : Alger et Oran.
Karim veut pouvoir lister rapidement les bons d'un seul entrepôt.
« Mais attention, me dit-il, dans S3 les dossiers n'existent pas vraiment… »</p>`,
		Objective: `Dans <code>livrexpress-factures</code>, déposer <strong>au moins 3</strong> objets sous le préfixe
<code>bons-livraison/alger/</code> et <strong>au moins 2</strong> sous <code>bons-livraison/oran/</code>.`,
		Concepts: []string{"Préfixe", "Délimiteur", "ListObjectsV2", "Upload récursif"},
		Docs:     []Link{{"AWS — Organiser avec des préfixes", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-prefixes.html"}},
		Steps: []Step{
			{
				Title:   "Comprendre les préfixes",
				Explain: `<p>Une clé <code>bons-livraison/alger/BL-17.pdf</code> est une chaîne unique. La console affiche des dossiers en découpant sur <code>/</code> (le <em>délimiteur</em>). Lister « un dossier » revient à lister les clés qui commencent par un préfixe.</p>`,
				Hints: []string{
					"Les fichiers peuvent être de simples textes : le contenu n'est pas vérifié ici, seules les clés comptent.",
					"Créez d'abord un dossier local contenant plusieurs fichiers : il existe un moyen d'envoyer un dossier entier d'un coup.",
				},
			},
			{
				Title:   "Envoyer plusieurs objets d'un coup",
				Explain: `<p>Envoyer les fichiers un par un fonctionne, mais un outil pro sait synchroniser ou copier récursivement un dossier.</p>`,
				Hints: []string{
					"<code>aws s3 cp</code> accepte une option pour descendre dans les sous-dossiers ; <code>aws s3 sync</code> fait cela par nature.",
					"Avec <code>mc</code>, la copie récursive utilise la même option que <code>cp</code> sous Linux… en version longue.",
					"Vérifiez votre résultat en listant le préfixe : <code>aws s3 ls s3://bucket/bons-livraison/ --recursive</code> (sans oublier l'endpoint).",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			na, err := countPrefix(ctx, c, c.N(bucketFactures), "bons-livraison/alger/")
			if !r.ok("Le bucket est accessible", err == nil, errDetail(err)) {
				return r.pending("≥ 3 objets sous bons-livraison/alger/", "≥ 2 objets sous bons-livraison/oran/")
			}
			no, _ := countPrefix(ctx, c, c.N(bucketFactures), "bons-livraison/oran/")
			r.ok("≥ 3 objets sous bons-livraison/alger/", na >= 3, "%d trouvé(s)", na)
			r.ok("≥ 2 objets sous bons-livraison/oran/", no >= 2, "%d trouvé(s)", no)
			return r.items
		},
	}
}

// --- Défi 4 ----------------------------------------------------------------

func s3Metadata() *Challenge {
	return &Challenge{
		ID: "s3-metadata", Title: "L'étiquette du colis", XP: 120,
		Story: `<p>L'équipe Data veut trier les factures sans les ouvrir. Idée : coller une
« étiquette » sur chaque objet. S3 permet d'attacher des <strong>métadonnées</strong>
système (type de contenu…) et utilisateur (<code>x-amz-meta-*</code>).</p>`,
		Objective: `Déposer <code>factures/2026/10/F-0002.json</code> avec :
<ul><li>le type de contenu <code>application/json</code> ;</li>
<li>une métadonnée utilisateur <code>client</code> non vide ;</li>
<li>une métadonnée utilisateur <code>entrepot</code> valant <code>alger</code>.</li></ul>`,
		Concepts: []string{"Content-Type", "Métadonnées utilisateur", "HeadObject"},
		Docs:     []Link{{"AWS — Métadonnées d'objet", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/UsingMetadata.html"}},
		Steps: []Step{
			{
				Title:   "Distinguer les deux types de métadonnées",
				Explain: `<p>Les métadonnées <strong>système</strong> (Content-Type, Cache-Control…) sont interprétées par S3 et les navigateurs. Les métadonnées <strong>utilisateur</strong> sont libres et transmises comme en-têtes HTTP <code>x-amz-meta-&lt;nom&gt;</code>.</p>`,
				Hints: []string{
					"Les métadonnées sont fixées au moment de l'écriture de l'objet : on ne peut pas les modifier sans réécrire (ou recopier) l'objet.",
					"Dans l'AWS CLI, cherchez dans l'aide de <code>put-object</code> (ou de <code>s3 cp</code>) les options <code>--content-type</code> et <code>--metadata</code>.",
					"Le format de <code>--metadata</code> est une liste <code>clé=valeur</code> séparée par des virgules. N'écrivez pas le préfixe <code>x-amz-meta-</code> vous-même : la CLI l'ajoute.",
				},
			},
			{
				Title:   "Vérifier sans télécharger",
				Explain: `<p>L'opération <strong>HeadObject</strong> renvoie uniquement les en-têtes d'un objet, pas son contenu. Idéal pour contrôler vos métadonnées.</p>`,
				Hints: []string{
					"<code>aws s3api head-object</code> attend un bucket et une clé.",
					"Avec mc : la commande <code>stat</code> affiche aussi les métadonnées.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			h, err := c.S3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(c.N(bucketFactures)), Key: aws.String(keyFacture2)})
			if !r.ok("L'objet "+keyFacture2+" existe", err == nil, errDetail(err)) {
				return r.pending("Content-Type = application/json", "Métadonnée client non vide", "Métadonnée entrepot = alger")
			}
			ct := aws.ToString(h.ContentType)
			r.ok("Content-Type = application/json", strings.HasPrefix(ct, "application/json"), "valeur actuelle : %q", ct)
			md := map[string]string{}
			for k, v := range h.Metadata {
				md[strings.ToLower(k)] = v
			}
			r.ok("Métadonnée client non vide", strings.TrimSpace(md["client"]) != "", "métadonnées trouvées : %v", keys(md))
			r.ok("Métadonnée entrepot = alger", strings.EqualFold(md["entrepot"], "alger"), "valeur actuelle : %q", md["entrepot"])
			return r.items
		},
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- Défi 5 ----------------------------------------------------------------

func s3Versioning() *Challenge {
	return &Challenge{
		ID: "s3-versioning", Title: "La machine à remonter le temps", XP: 180,
		Story: `<p>Le contrat de transport avec le partenaire maritime est modifié chaque trimestre.
Le juriste exige de conserver <strong>toutes</strong> les versions. Et Karim vous prévient :
« Le stagiaire a la fâcheuse habitude de supprimer des fichiers par erreur… »</p>`,
		Objective: `<ol><li>Créer le bucket <code>livrexpress-archives</code> avec le <strong>versioning activé</strong>.</li>
<li>Y déposer <code>contrats/contrat-transport.txt</code> puis le remplacer par une 2ᵉ version.</li>
<li>Cliquer sur « Simuler l'incident » : le stagiaire supprime le contrat.</li>
<li><strong>Restaurer</strong> le contrat pour qu'il soit de nouveau lisible.</li></ol>`,
		Concepts: []string{"Versioning", "VersionId", "Delete marker", "Restauration"},
		Docs:     []Link{{"AWS — Versioning", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/Versioning.html"}},
		Setup: &SetupSpec{
			Label: "💥 Simuler l'incident (suppression du contrat)",
			Run: func(ctx context.Context, c *cloud.Clients, st *ChallengeState) (string, error) {
				v, err := c.S3.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(c.N(bucketArchives))})
				if err != nil {
					return "", fmt.Errorf("le bucket %s est introuvable : créez-le d'abord", bucketArchives)
				}
				if v.Status != types.BucketVersioningStatusEnabled {
					return "", errors.New("le stagiaire s'apprêtait à supprimer le contrat… mais sans versioning, ce serait définitif ! Activez d'abord le versioning")
				}
				vers, err := c.S3.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(c.N(bucketArchives)), Prefix: aws.String(keyContrat)})
				if err != nil {
					return "", err
				}
				if len(vers.Versions) < 2 {
					return "", fmt.Errorf("il faut au moins 2 versions du contrat avant l'incident (%d trouvée(s))", len(vers.Versions))
				}
				if _, err := c.S3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(c.N(bucketArchives)), Key: aws.String(keyContrat)}); err != nil {
					return "", err
				}
				st.Secret["incident"] = "1"
				return "Oups ! Le stagiaire vient de supprimer contrats/contrat-transport.txt. Le juriste arrive dans 10 minutes… À vous de jouer.", nil
			},
		},
		Steps: []Step{
			{
				Title:   "Activer le versioning",
				Explain: `<p>Le versioning se règle au niveau du <strong>bucket</strong>. Une fois activé, il ne peut plus être désactivé, seulement <em>suspendu</em>.</p>`,
				Hints: []string{
					"La console MinIO propose un interrupteur dans les paramètres du bucket.",
					"Côté CLI AWS, la commande bas niveau <code>s3api</code> possède une opération <code>put-bucket-versioning</code> ; regardez le format de <code>--versioning-configuration</code>.",
					"Avec mc, cherchez une sous-commande nommée <code>version</code>.",
				},
			},
			{
				Title:   "Créer deux versions",
				Explain: `<p>Écrire deux fois la même clé dans un bucket versionné crée deux versions distinctes, chacune avec son <code>VersionId</code>.</p>`,
				Hints: []string{
					"Modifiez le contenu du fichier local entre les deux envois pour bien distinguer les versions.",
					"<code>aws s3api list-object-versions</code> (ou <code>mc ls --versions</code>) affiche l'historique.",
				},
			},
			{
				Title:   "Restaurer après l'incident",
				Explain: `<p>Après une suppression « simple », l'objet semble disparu : un <strong>delete marker</strong> est devenu sa version courante. Mais les données sont toujours là…</p>`,
				Hints: []string{
					"Listez les versions : repérez l'entrée de type <em>DeleteMarker</em> et son <code>VersionId</code>.",
					"Deux stratégies : supprimer le marqueur lui-même (en ciblant son VersionId), ou recopier une ancienne version par-dessus la clé.",
					"<code>delete-object</code> accepte <code>--version-id</code>. Une suppression ciblant la version d'un marqueur… supprime le marqueur.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, st *ChallengeState) []CheckItem {
			r := &report{}
			v, err := c.S3.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(c.N(bucketArchives))})
			if !r.ok("Le bucket livrexpress-archives existe", err == nil, errDetail(err)) {
				return r.pending("Versioning activé", "≥ 2 versions du contrat", "Incident simulé", "Contrat restauré et lisible")
			}
			r.ok("Versioning activé", v.Status == types.BucketVersioningStatusEnabled, "statut actuel : %q", string(v.Status))
			vers, err := c.S3.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(c.N(bucketArchives)), Prefix: aws.String(keyContrat)})
			n := 0
			if err == nil {
				for _, ov := range vers.Versions {
					if aws.ToString(ov.Key) == keyContrat {
						n++
					}
				}
			}
			r.ok("≥ 2 versions du contrat", n >= 2, "%d version(s) de données", n)
			if !r.ok("Incident simulé", st.Secret["incident"] == "1", "cliquez sur « Simuler l'incident » une fois les 2 versions créées") {
				return r.pending("Contrat restauré et lisible")
			}
			_, err = c.S3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(c.N(bucketArchives)), Key: aws.String(keyContrat)})
			r.ok("Contrat restauré et lisible", err == nil, "l'objet est toujours masqué par un delete marker")
			return r.items
		},
	}
}

// --- Défi 6 ----------------------------------------------------------------

func s3Lifecycle() *Challenge {
	return &Challenge{
		ID: "s3-lifecycle", Title: "Le ménage automatique", XP: 150,
		Story: `<p>Les outils d'export déposent des fichiers temporaires sous <code>tmp/</code>.
Personne ne les nettoie et la facture de stockage grimpe. Karim : « Je ne veux pas d'un
script cron. Le stockage doit faire le ménage tout seul. »</p>`,
		Objective: `Sur <code>livrexpress-factures</code>, configurer une règle de cycle de vie <strong>active</strong>
qui <strong>expire</strong> les objets du préfixe <code>tmp/</code> au bout de <strong>7 jours</strong>.`,
		Concepts: []string{"Lifecycle", "Expiration", "Filtre par préfixe", "FinOps"},
		Docs:     []Link{{"AWS — Cycle de vie", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/object-lifecycle-mgmt.html"}},
		Steps: []Step{
			{
				Title:   "Concevoir la règle",
				Explain: `<p>Une règle de cycle de vie combine : un <strong>filtre</strong> (quels objets ?), un <strong>statut</strong> (active ou non) et des <strong>actions</strong> (expiration, transition…).</p>`,
				Hints: []string{
					"Votre filtre porte sur un préfixe ; votre action est une expiration exprimée en jours.",
					"Écrivez la configuration dans un fichier JSON : c'est plus lisible que tout sur une ligne.",
				},
			},
			{
				Title:   "Appliquer la règle",
				Explain: `<p>La configuration de cycle de vie remplace entièrement la précédente à chaque écriture.</p>`,
				Hints: []string{
					"L'opération S3 s'appelle <code>PutBucketLifecycleConfiguration</code>. Dans la CLI : <code>aws s3api put-bucket-lifecycle-configuration</code>.",
					"Le JSON attendu a une clé racine <code>Rules</code> (une liste). Chaque règle contient <code>ID</code>, <code>Status</code>, <code>Filter</code> et <code>Expiration</code>.",
					"Avec mc : <code>mc ilm rule add</code> et ses options <code>--prefix</code> et <code>--expire-days</code>. Vérifiez ensuite avec <code>mc ilm rule ls</code>.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			lc, err := c.S3.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(c.N(bucketFactures))})
			if !r.ok("Une configuration de cycle de vie existe", err == nil, errDetail(err)) {
				return r.pending("Règle active sur le préfixe tmp/", "Expiration à 7 jours")
			}
			var match *types.LifecycleRule
			for i, rule := range lc.Rules {
				p := aws.ToString(rule.Prefix) //nolint:staticcheck // ancien format toléré
				if rule.Filter != nil {
					if rule.Filter.Prefix != nil {
						p = *rule.Filter.Prefix
					} else if rule.Filter.And != nil && rule.Filter.And.Prefix != nil {
						p = *rule.Filter.And.Prefix
					}
				}
				if p == "tmp/" && rule.Status == types.ExpirationStatusEnabled {
					match = &lc.Rules[i]
				}
			}
			if !r.ok("Règle active sur le préfixe tmp/", match != nil, "%d règle(s) trouvée(s), aucune active sur exactement « tmp/ »", len(lc.Rules)) {
				return r.pending("Expiration à 7 jours")
			}
			days := int32(0)
			if match.Expiration != nil && match.Expiration.Days != nil {
				days = *match.Expiration.Days
			}
			r.ok("Expiration à 7 jours", days == 7, "expiration actuelle : %d jour(s)", days)
			return r.items
		},
	}
}

// --- Défi 7 ----------------------------------------------------------------

func s3Presigned() *Challenge {
	return &Challenge{
		ID: "s3-presign", Title: "Le lien qui s'autodétruit", XP: 150,
		Story: `<p>La Pharmacie El Amel demande une copie de sa facture F-0001. Hors de question de
rendre le bucket public, ni de donner des identifiants au client. Il faut un lien
<strong>temporaire</strong>, valable au plus une heure.</p>`,
		Objective: `Générer une <strong>URL pré-signée</strong> de lecture pour <code>factures/2026/10/F-0001.json</code>,
valable <strong>1 heure maximum</strong>, et la coller ci-dessous. Le bucket doit rester privé.`,
		Concepts: []string{"Signature V4", "Accès temporaire", "Moindre privilège"},
		Docs:     []Link{{"AWS — URL pré-signées", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/ShareObjectPreSignedURL.html"}},
		Input:    &InputSpec{Label: "URL pré-signée", Placeholder: "http://127.0.0.1:9100/livrexpress-factures/factures/...?X-Amz-Algorithm=..."},
		Steps: []Step{
			{
				Title:   "Comprendre la signature",
				Explain: `<p>Une URL pré-signée contient dans ses paramètres : l'algorithme, l'identifiant de clé, la date, la <strong>durée de validité</strong> et une <strong>signature</strong> HMAC. Le serveur recalcule la signature : toute modification de l'URL l'invalide.</p>`,
				Hints: []string{
					"Repérez dans une URL pré-signée le paramètre <code>X-Amz-Expires</code> : c'est la durée en secondes.",
					"Une heure = 3600 secondes. Par défaut, les outils proposent souvent une durée bien plus longue.",
				},
			},
			{
				Title:   "Générer l'URL",
				Explain: `<p>La génération se fait <strong>hors-ligne</strong> : aucun appel au serveur n'est nécessaire, seulement vos clés.</p>`,
				Hints: []string{
					"L'AWS CLI possède une commande <code>aws s3 presign</code> ; regardez l'option qui règle l'expiration.",
					"Avec mc : <code>mc share download</code> et son option <code>--expire</code> (ex. <code>30m</code>).",
					"L'hôte de l'URL doit être celui que vous avez utilisé (127.0.0.1:9100 ou localhost:9100) : la signature dépend aussi de l'hôte !",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, answer string, _ *ChallengeState) []CheckItem {
			r := &report{}
			u, err := url.Parse(strings.TrimSpace(answer))
			if !r.ok("URL lisible", err == nil && u.Scheme != "" && u.Host != "", "collez l'URL complète, http:// compris") {
				return r.pending("Pointe vers MinIO local", "URL signée (Signature V4)", "Vise la facture F-0001", "Validité ≤ 1 heure", "Le lien fonctionne", "Le bucket reste privé")
			}
			ep, _ := url.Parse(c.Cfg.S3Endpoint)
			port := ep.Port()
			okHost := (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost" || u.Hostname() == ep.Hostname()) && u.Port() == port
			expected := fmt.Sprintf("127.0.0.1:%s ou localhost:%s", port, port)
			if pub, _ := url.Parse(c.Cfg.S3Public); c.Cfg.Hosted && pub != nil {
				// Plateforme partagée : l'URL est signée pour l'adresse publique.
				okHost, expected = u.Host == pub.Host, pub.Host
			}
			if !r.ok("Pointe vers MinIO local", okHost, "hôte attendu : %s", expected) {
				return r.pending("URL signée (Signature V4)", "Vise la facture F-0001", "Validité ≤ 1 heure", "Le lien fonctionne", "Le bucket reste privé")
			}
			q := u.Query()
			r.ok("URL signée (Signature V4)", q.Get("X-Amz-Signature") != "" && q.Get("X-Amz-Algorithm") == "AWS4-HMAC-SHA256", "paramètres X-Amz-Signature / X-Amz-Algorithm absents")
			r.ok("Vise la facture F-0001", strings.TrimPrefix(u.Path, "/") == c.N(bucketFactures)+"/"+keyFacture1, "chemin trouvé : %s", u.Path)
			exp, _ := strconv.Atoi(q.Get("X-Amz-Expires"))
			r.ok("Validité ≤ 1 heure", exp > 0 && exp <= 3600, "X-Amz-Expires = %d s", exp)
			code, err := anonGet(c, u.String())
			r.ok("Le lien fonctionne", err == nil && code == http.StatusOK, "réponse HTTP %d %s", code, errDetail(err))
			plain := *u
			plain.RawQuery = ""
			code, _ = anonGet(c, plain.String())
			r.ok("Le bucket reste privé", code == http.StatusForbidden, "sans signature, l'objet répond HTTP %d (attendu 403)", code)
			return r.items
		},
	}
}

// --- Défi 8 ----------------------------------------------------------------

func s3Policy() *Challenge {
	return &Challenge{
		ID: "s3-policy", Title: "La vitrine et l'arrière-boutique", XP: 200,
		Story: `<p>Le service marketing veut héberger le mini-site de suivi de colis dans S3. Le dossier
<code>public/</code> doit être lisible par tout Internet… mais dans le même bucket traîne
<code>prive/tarifs-negocies.csv</code>, que les concurrents adoreraient lire.</p>`,
		Objective: `<ul><li>Créer le bucket <code>livrexpress-site</code> avec les objets <code>public/index.html</code> et <code>prive/tarifs-negocies.csv</code>.</li>
<li>Rendre <strong>uniquement</strong> les objets de <code>public/</code> lisibles anonymement.</li>
<li>Le reste doit rester privé, et le contenu du bucket ne doit <strong>pas</strong> être listable anonymement.</li></ul>`,
		Concepts: []string{"Bucket policy", "IAM JSON", "Principal", "Resource ARN", "Moindre privilège"},
		Docs:     []Link{{"AWS — Exemples de politiques de bucket", "https://docs.aws.amazon.com/AmazonS3/latest/userguide/example-bucket-policies.html"}},
		Steps: []Step{
			{
				Title:   "Préparer le bucket et les objets",
				Explain: `<p>Commencez par la partie que vous maîtrisez déjà : un bucket, deux objets.</p>`,
				Hints:   []string{"Le contenu des fichiers est libre : une ligne de HTML et une ligne de CSV suffisent."},
			},
			{
				Title:   "Écrire la politique",
				Explain: `<p>Une politique est un document JSON composé de <strong>déclarations</strong> (<em>Statement</em>). Chacune répond à : <em>qui</em> (Principal), <em>peut faire quoi</em> (Action), <em>sur quoi</em> (Resource), avec quel <em>effet</em> (Allow/Deny).</p>`,
				Hints: []string{
					"« Tout le monde » s'écrit avec un Principal joker. L'action de lecture d'un objet est <code>s3:GetObject</code>.",
					"Les ARN S3 ont la forme <code>arn:aws:s3:::bucket/clé</code>. Les jokers <code>*</code> sont autorisés dans la partie clé.",
					"Lister un bucket est une autre action (<code>s3:ListBucket</code>) portant sur l'ARN du bucket lui-même : ne l'accordez pas !",
				},
			},
			{
				Title:   "Appliquer et tester comme un inconnu",
				Explain: `<p>Testez toujours une politique depuis l'extérieur, sans identifiants : un simple <code>curl</code> suffit.</p>`,
				Hints: []string{
					"<code>aws s3api put-bucket-policy</code> prend <code>--policy file://fichier.json</code>.",
					"Avec mc, <code>mc anonymous set download</code> vise un chemin… mais attention à ne pas ouvrir tout le bucket ni la liste !",
					"<code>curl -i http://127.0.0.1:9100/livrexpress-site/public/index.html</code> doit répondre 200 ; la même chose sur <code>prive/</code> doit répondre 403.",
				},
			},
		},
		Check: func(ctx context.Context, c *cloud.Clients, _ string, _ *ChallengeState) []CheckItem {
			r := &report{}
			err := bucketExists(ctx, c, c.N(bucketSite))
			if !r.ok("Le bucket livrexpress-site existe", err == nil, errDetail(err)) {
				return r.pending("Les deux objets existent", "public/index.html lisible anonymement", "prive/tarifs-negocies.csv protégé", "Liste anonyme du bucket refusée")
			}
			_, e1 := c.S3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(c.N(bucketSite)), Key: aws.String("public/index.html")})
			_, e2 := c.S3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(c.N(bucketSite)), Key: aws.String("prive/tarifs-negocies.csv")})
			if !r.ok("Les deux objets existent", e1 == nil && e2 == nil, "public/index.html: %v — prive/tarifs-negocies.csv: %v", e1 == nil, e2 == nil) {
				return r.pending("public/index.html lisible anonymement", "prive/tarifs-negocies.csv protégé", "Liste anonyme du bucket refusée")
			}
			base := strings.TrimRight(c.Cfg.S3Endpoint, "/") + "/" + c.N(bucketSite)
			code, _ := anonGet(c, base+"/public/index.html")
			r.ok("public/index.html lisible anonymement", code == 200, "HTTP %d (attendu 200)", code)
			code, _ = anonGet(c, base+"/prive/tarifs-negocies.csv")
			r.ok("prive/tarifs-negocies.csv protégé", code == 403, "HTTP %d (attendu 403)", code)
			code, _ = anonGet(c, base+"/")
			r.ok("Liste anonyme du bucket refusée", code == 403, "HTTP %d (attendu 403)", code)
			return r.items
		},
	}
}
