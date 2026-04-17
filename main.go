package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
)

// --------------- Data Store ---------------

type User struct {
	ID        string `json:"id"`
	SlackUID  string `json:"slack_uid"`
	Name      string `json:"name"`
	Token     string `json:"token"`
	CheckedIn bool   `json:"checked_in"`
}

type Store struct {
	mu      sync.Mutex
	users   map[string]*User
	bySlack map[string]string
	path    string
}

func NewStore(path string) *Store {
	s := &Store{
		users:   make(map[string]*User),
		bySlack: make(map[string]string),
		path:    path,
	}
	s.load()
	return s
}

func (s *Store) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var users []*User
	if err := json.Unmarshal(data, &users); err != nil {
		log.Printf("users.json 読み込みエラー: %v", err)
		return
	}
	for _, u := range users {
		s.users[u.ID] = u
		s.bySlack[u.SlackUID] = u.ID
	}
	log.Printf("%d 人のユーザーを読み込みました", len(s.users))
}

func (s *Store) save() {
	users := make([]*User, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
	}
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		log.Printf("users.json 保存エラー: %v", err)
		return
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		log.Printf("users.json 書き込みエラー: %v", err)
	}
}

func (s *Store) Upsert(slackUID, name, token string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id, ok := s.bySlack[slackUID]; ok {
		u := s.users[id]
		u.Token = token
		u.Name = name
		s.save()
		return u
	}

	id := generateID()
	u := &User{
		ID:       id,
		SlackUID: slackUID,
		Name:     name,
		Token:    token,
	}
	s.users[id] = u
	s.bySlack[slackUID] = id
	s.save()
	return u
}

func (s *Store) Toggle(id string) (user *User, action string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, exists := s.users[id]
	if !exists {
		return nil, "", false
	}

	action = "hi"
	if u.CheckedIn {
		action = "bye"
	}
	u.CheckedIn = !u.CheckedIn
	s.save()
	return u, action, true
}

func (s *Store) Revert(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[id]; ok {
		u.CheckedIn = !u.CheckedIn
		s.save()
	}
}

func generateID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// --------------- Config ---------------

var (
	store        *Store
	channelID    string
	clientID     string
	clientSecret string
	baseURL      string
)

// --------------- Handlers ---------------

func handleRegister(w http.ResponseWriter, r *http.Request) {
	authURL := fmt.Sprintf(
		"https://slack.com/oauth/v2/authorize?client_id=%s&user_scope=%s&redirect_uri=%s",
		clientID,
		url.QueryEscape("chat:write,users:read"),
		url.QueryEscape(baseURL+"/auth/callback"),
	)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>NFC 出退勤 - 登録</title>
	<style>
		*{margin:0;padding:0;box-sizing:border-box}
		body{display:flex;justify-content:center;align-items:center;height:100vh;
			font-family:-apple-system,sans-serif;background:#f5f5f5}
		.container{text-align:center;width:90%%;max-width:360px}
		h1{font-size:1.4rem;margin-bottom:.5rem;color:#333}
		p{color:#666;margin-bottom:2rem;font-size:.9rem;line-height:1.6}
		.btn{display:inline-block;padding:1rem 2rem;background:#4A154B;color:#fff;
			border-radius:8px;text-decoration:none;font-size:1.1rem;font-weight:bold}
	</style>
</head>
<body>
	<div class="container">
		<h1>NFC 出退勤</h1>
		<p>Slackアカウントと連携して<br>あなた専用のNFC URLを発行します。</p>
		<a class="btn" href="%s">Slackで登録する</a>
	</div>
</body>
</html>`, authURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func handleCallback(w http.ResponseWriter, r *http.Request) {
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		http.Error(w, "Slack認証がキャンセルされました: "+errMsg, http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "認証コードがありません", http.StatusBadRequest)
		return
	}

	resp, err := slack.GetOAuthV2Response(http.DefaultClient, clientID, clientSecret, code, baseURL+"/auth/callback")
	if err != nil {
		log.Printf("OAuth エラー: %v", err)
		http.Error(w, "Slack認証に失敗しました", http.StatusInternalServerError)
		return
	}

	token := resp.AuthedUser.AccessToken
	slackUID := resp.AuthedUser.ID

	name := slackUID
	api := slack.New(token)
	if info, err := api.GetUserInfo(slackUID); err == nil {
		name = info.RealName
		if name == "" {
			name = info.Name
		}
	}

	user := store.Upsert(slackUID, name, token)
	nfcURL := fmt.Sprintf("%s/t/%s", baseURL, user.ID)

	log.Printf("ユーザー登録: %s (%s) → %s", name, slackUID, user.ID)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>登録完了</title>
	<style>
		*{margin:0;padding:0;box-sizing:border-box}
		body{display:flex;justify-content:center;align-items:center;min-height:100vh;
			font-family:-apple-system,sans-serif;background:#f5f5f5;padding:1rem}
		.container{text-align:center;width:100%%;max-width:400px}
		h2{font-size:1.3rem;color:#333;margin-bottom:1.5rem}
		.url-box{background:#fff;border:2px solid #4CAF50;border-radius:8px;
			padding:1rem;margin-bottom:1rem;word-break:break-all;
			font-family:monospace;font-size:.85rem}
		.note{color:#888;font-size:.8rem;line-height:1.6}
	</style>
</head>
<body>
	<div class="container">
		<h2>%s さんの登録が完了しました</h2>
		<p style="margin-bottom:1rem;color:#666;">このURLをNFCタグに書き込んでください：</p>
		<div class="url-box">%s</div>
		<p class="note">
			このURLにアクセスするたびに<br>
			hi → bye → hi → ... と自動で切り替わります。
		</p>
	</div>
</body>
</html>`, name, nfcURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func handleAttendance(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/t/")
	if id == "" {
		http.Error(w, "ユーザーIDがありません", http.StatusBadRequest)
		return
	}

	user, action, ok := store.Toggle(id)
	if !ok {
		http.Error(w, "ユーザーが見つかりません。/register から登録してください。", http.StatusNotFound)
		return
	}

	api := slack.New(user.Token)
	_, _, err := api.PostMessage(channelID, slack.MsgOptionText(action, false))
	if err != nil {
		log.Printf("送信エラー (%s): %v", user.Name, err)
		store.Revert(id)
		http.Error(w, "Slackへの送信に失敗しました", http.StatusInternalServerError)
		return
	}

	label := "出勤 (hi)"
	color := "#4CAF50"
	if action == "bye" {
		label = "退勤 (bye)"
		color = "#2196F3"
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>送信完了</title>
	<style>
		*{margin:0;padding:0;box-sizing:border-box}
		body{display:flex;justify-content:center;align-items:center;height:100vh;
			font-family:-apple-system,sans-serif;background:%s;color:#fff}
		.container{text-align:center}
		h2{font-size:1.6rem;margin-bottom:.5rem}
		p{opacity:.8}
	</style>
</head>
<body>
	<div class="container">
		<h2>%s</h2>
		<p>%s さん</p>
		<p style="margin-top:1rem">この画面は自動的に閉じます。</p>
	</div>
	<script>setTimeout(function(){ window.close(); }, 2000);</script>
</body>
</html>`, color, label, user.Name)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// --------------- Main ---------------

func main() {
	_ = godotenv.Load()

	channelID = os.Getenv("SLACK_CHANNEL_ID")
	clientID = os.Getenv("SLACK_CLIENT_ID")
	clientSecret = os.Getenv("SLACK_CLIENT_SECRET")
	baseURL = strings.TrimRight(os.Getenv("BASE_URL"), "/")

	if channelID == "" || clientID == "" || clientSecret == "" || baseURL == "" {
		log.Fatal("環境変数を設定してください: SLACK_CHANNEL_ID, SLACK_CLIENT_ID, SLACK_CLIENT_SECRET, BASE_URL")
	}

	store = NewStore("users.json")

	http.HandleFunc("/", handleRegister)
	http.HandleFunc("/register", handleRegister)
	http.HandleFunc("/auth/callback", handleCallback)
	http.HandleFunc("/t/", handleAttendance)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("サーバーを起動しました: http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
