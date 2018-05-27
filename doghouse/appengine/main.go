package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/haya14busa/reviewdog/doghouse"
	"github.com/haya14busa/reviewdog/doghouse/server"
	"github.com/haya14busa/reviewdog/doghouse/server/cookieman"
	"github.com/haya14busa/secretbox"
	"google.golang.org/appengine"
	"google.golang.org/appengine/urlfetch"
)

func mustCookieMan() *cookieman.CookieMan {
	// Create secret key by following command.
	// $ ruby -rsecurerandom -e 'puts SecureRandom.hex(32)'
	cipher, err := secretbox.NewFromHexKey(mustGetenv("SECRETBOX_SECRET"))
	if err != nil {
		log.Fatalf("failed to create secretbox: %v", err)
	}
	c := cookieman.CookieOption{
		http.Cookie{
			HttpOnly: true,
			Secure:   !appengine.IsDevAppServer(),
			MaxAge:   int((30 * 24 * time.Hour).Seconds()),
			Path:     "/",
		},
	}
	if !appengine.IsDevAppServer() {
		c.Secure = true
		c.Domain = "review-dog.appspot.com"
	}
	return cookieman.New(cipher, c)
}

func mustGitHubAppsPrivateKey() []byte {
	// Private keys https://github.com/settings/apps/reviewdog
	const privateKeyFile = "./secret/github-apps.private-key.pem"
	githubAppsPrivateKey, err := ioutil.ReadFile(privateKeyFile)
	if err != nil {
		log.Fatalf("could not read private key: %s", err)
	}
	return githubAppsPrivateKey
}

func mustGetenv(name string) string {
	s := os.Getenv(name)
	if s == "" {
		log.Fatalf("%s is not set", name)
	}
	return s
}

func mustIntEnv(name string) int {
	s := os.Getenv(name)
	if s == "" {
		log.Fatalf("%s is not set", name)
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		log.Fatal(err)
	}
	return i
}

func main() {
	integrationID := mustIntEnv("GITHUB_INTEGRATION_ID")
	ghPrivateKey := mustGitHubAppsPrivateKey()

	ghHandler := NewGitHubHandler(
		mustGetenv("GITHUB_CLIENT_ID"),
		mustGetenv("GITHUB_CLIENT_SECRET"),
		mustCookieMan(),
		ghPrivateKey,
		integrationID,
	)

	ghChecker := &githubChecker{
		privateKey:    ghPrivateKey,
		integrationID: integrationID,
	}

	ghWebhookHandler := &githubWebhookHandler{
		secret: []byte(mustGetenv("GITHUB_WEBHOOK_SECRET")),
	}

	http.HandleFunc("/", handleTop)
	http.HandleFunc("/check", ghChecker.handleCheck)
	http.HandleFunc("/webhook", ghWebhookHandler.handleWebhook)
	http.HandleFunc("/gh/_auth/callback", ghHandler.HandleAuthCallback)
	http.Handle("/gh/", ghHandler.Handler(http.HandlerFunc(ghHandler.HandleGitHubTop)))
	appengine.Main()
}

func handleTop(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "reviewdog")
}

type githubChecker struct {
	privateKey    []byte
	integrationID int
}

func (gc *githubChecker) handleCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req doghouse.CheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "failed to decode request: %v", err)
		return
	}
	ctx := appengine.NewContext(r)

	opt := &server.NewGitHubClientOption{
		PrivateKey:     gc.privateKey,
		IntegrationID:  gc.integrationID,
		InstallationID: req.InstallationID,
		RepoOwner:      req.Owner,
		RepoName:       req.Repo,
		Client:         urlfetch.Client(ctx),
	}

	gh, err := server.NewGitHubClient(ctx, opt)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, err)
		return
	}

	res, err := server.NewChecker(&req, gh).Check(ctx)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, err)
		return
	}
	if err := json.NewEncoder(w).Encode(res); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintln(w, err)
		return
	}
}