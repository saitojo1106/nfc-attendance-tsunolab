package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

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
	mu       sync.Mutex
	users    map[string]*User
	bySlack  map[string]string
	lastTap  map[string]time.Time
	path     string
}

func NewStore(path string) *Store {
	s := &Store{
		users:   make(map[string]*User),
		bySlack: make(map[string]string),
		lastTap: make(map[string]time.Time),
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

func (s *Store) Get(id string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.users[id]
}

const tapCooldown = 10 * time.Second

func (s *Store) Toggle(id string) (user *User, action string, ok bool, duplicate bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, exists := s.users[id]
	if !exists {
		return nil, "", false, false
	}

	if last, seen := s.lastTap[id]; seen && time.Since(last) < tapCooldown {
		action = "hi"
		if u.CheckedIn {
			action = "bye"
		}
		// CheckedIn は前回のToggleで既に反転済みなので、現在の状態の逆が「前回送ったもの」
		prevAction := "hi"
		if !u.CheckedIn {
			prevAction = "bye"
		}
		_ = action
		return u, prevAction, true, true
	}
	s.lastTap[id] = time.Now()

	action = "hi"
	if u.CheckedIn {
		action = "bye"
	}
	u.CheckedIn = !u.CheckedIn
	s.save()
	return u, action, true, false
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

const hiByeBotOAuthURL = "https://slack.com/oauth/v2/authorize?client_id=2891184249.6899350542852&scope=&user_scope=chat:write"
const cookieName = "nfc_uid"
const cookieMaxAge = 365 * 24 * 60 * 60 // 1年

var (
	store     *Store
	channelID string
	baseURL   string
)

// --------------- Handlers ---------------

// GET / — Cookieでユーザー判定。登録済みならhi/bye送信、未登録なら登録画面へ
func handleTop(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(cookieName)
	if err != nil || cookie.Value == "" {
		http.Redirect(w, r, "/register", http.StatusFound)
		return
	}

	user := store.Get(cookie.Value)
	if user == nil {
		http.Redirect(w, r, "/register", http.StatusFound)
		return
	}

	sendAttendance(w, r, cookie.Value)
}

// GET /register — 登録フォーム
func handleRegister(w http.ResponseWriter, r *http.Request) {
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>NFC 出退勤 - 登録</title>
	<style>
		*{margin:0;padding:0;box-sizing:border-box}
		body{display:flex;justify-content:center;align-items:center;min-height:100vh;
			font-family:-apple-system,sans-serif;background:#f5f5f5;padding:1rem}
		.container{text-align:center;width:90%%;max-width:400px}
		h1{font-size:1.4rem;margin-bottom:1rem;color:#333}
		.steps{text-align:left;background:#fff;border-radius:8px;padding:1.2rem;
			margin-bottom:1.5rem;font-size:.9rem;line-height:1.8;color:#444}
		.steps b{color:#333}
		.btn{display:inline-block;padding:.8rem 1.5rem;background:#4A154B;color:#fff;
			border-radius:8px;text-decoration:none;font-size:1rem;font-weight:bold;
			margin-bottom:1.5rem}
		form{background:#fff;border-radius:8px;padding:1.2rem}
		label{display:block;text-align:left;font-size:.85rem;color:#666;margin-bottom:.5rem}
		input[type=text]{width:100%%;padding:.7rem;border:1px solid #ddd;border-radius:6px;
			font-size:.9rem;margin-bottom:1rem;font-family:monospace}
		button{width:100%%;padding:.8rem;background:#4CAF50;color:#fff;border:none;
			border-radius:8px;font-size:1rem;font-weight:bold;cursor:pointer}
	</style>
</head>
<body>
	<div class="container">
		<h1>NFC 出退勤 - 初回登録</h1>
		<div class="steps">
			<b>Step 1:</b> 下のボタンからSlackでトークンを取得<br>
			<b>Step 2:</b> 表示されたJSONの <code>access_token</code> をコピー<br>
			<b>Step 3:</b> 下のフォームに貼り付けて登録
		</div>
		<a class="btn" href="%s" target="_blank">Slackでトークンを取得</a>
		<form method="POST" action="/register">
			<label>アクセストークン（xoxp- から始まる文字列）</label>
			<input type="text" name="token" placeholder="xoxp-..." required>
			<button type="submit">登録する</button>
		</form>
	</div>
</body>
</html>`, hiByeBotOAuthURL)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// POST /register — トークン検証・登録・Cookie発行
func handleRegisterPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "フォームの解析に失敗しました", http.StatusBadRequest)
		return
	}

	token := strings.TrimSpace(r.FormValue("token"))
	if !strings.HasPrefix(token, "xoxp-") {
		http.Error(w, "トークンは xoxp- から始まる文字列を入力してください", http.StatusBadRequest)
		return
	}

	api := slack.New(token)
	authResp, err := api.AuthTest()
	if err != nil {
		log.Printf("トークン検証エラー: %v", err)
		http.Error(w, "トークンが無効です。正しいトークンを入力してください。", http.StatusBadRequest)
		return
	}

	user := store.Upsert(authResp.UserID, authResp.User, token)

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    user.ID,
		Path:     "/",
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	log.Printf("ユーザー登録: %s (%s)", user.Name, user.SlackUID)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>登録完了</title>
	<style>
		*{margin:0;padding:0;box-sizing:border-box}
		body{display:flex;justify-content:center;align-items:center;height:100vh;
			font-family:-apple-system,sans-serif;background:#4CAF50;color:#fff}
		.container{text-align:center;width:90%%;max-width:400px}
		h2{font-size:1.4rem;margin-bottom:1rem}
		p{opacity:.9;line-height:1.6}
	</style>
</head>
<body>
	<div class="container">
		<h2>%s さんの登録が完了しました</h2>
		<p>次回からNFCタグをかざすだけで<br>自動的にhi/byeが送信されます。</p>
	</div>
</body>
</html>`, user.Name)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// hi/bye送信の共通処理
func sendAttendance(w http.ResponseWriter, r *http.Request, userID string) {
	user, action, ok, dup := store.Toggle(userID)
	if !ok {
		http.SetCookie(w, &http.Cookie{
			Name:   cookieName,
			Path:   "/",
			MaxAge: -1,
		})
		http.Redirect(w, r, "/register", http.StatusFound)
		return
	}

	if !dup {
		api := slack.New(user.Token)
		_, _, err := api.PostMessage(channelID, slack.MsgOptionText(action, false))
		if err != nil {
			log.Printf("送信エラー (%s): %v", user.Name, err)
			store.Revert(userID)
			http.Error(w, "Slackへの送信に失敗しました", http.StatusInternalServerError)
			return
		}
	}

	label := "出勤 (hi)"
	color := "#4CAF50"
	if action == "bye" {
		label = "退勤 (bye)"
		color = "#2196F3"
	}

	now := time.Now().In(time.FixedZone("JST", 9*60*60)).Format("15:04")

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
		h2{font-size:1.8rem;margin-bottom:.3rem}
		.time{font-size:3rem;font-weight:bold;margin:.5rem 0}
		p{opacity:.8;font-size:.9rem}
	</style>
</head>
<body>
	<div class="container">
		<h2>%s</h2>
		<div class="time">%s</div>
		<p>%s さん</p>
		<p style="margin-top:1rem">この画面は自動的に閉じます。</p>
	</div>
	<script>setTimeout(function(){ window.close(); }, 2000);</script>
</body>
</html>`, color, label, now, user.Name)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

// --------------- Main ---------------

func main() {
	_ = godotenv.Load()

	channelID = os.Getenv("SLACK_CHANNEL_ID")
	baseURL = strings.TrimRight(os.Getenv("BASE_URL"), "/")

	if channelID == "" || baseURL == "" {
		log.Fatal("環境変数を設定してください: SLACK_CHANNEL_ID, BASE_URL")
	}

	store = NewStore("users.json")

	http.HandleFunc("/", handleTop)
	http.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			handleRegisterPost(w, r)
		} else {
			handleRegister(w, r)
		}
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("サーバーを起動しました: http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
