package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
)

var api *slack.Client
var channelID string

const selectPage = `<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>出退勤</title>
	<style>
		* { margin: 0; padding: 0; box-sizing: border-box; }
		body {
			display: flex; justify-content: center; align-items: center;
			height: 100vh; font-family: -apple-system, sans-serif;
			background: #f5f5f5;
		}
		.container { text-align: center; width: 90%%; max-width: 360px; }
		h1 { font-size: 1.4rem; margin-bottom: 2rem; color: #333; }
		.btn {
			display: block; width: 100%%; padding: 1.2rem;
			margin-bottom: 1rem; border: none; border-radius: 12px;
			font-size: 1.3rem; font-weight: bold; color: #fff;
			cursor: pointer; text-decoration: none; text-align: center;
		}
		.btn-hi  { background: #4CAF50; }
		.btn-bye { background: #2196F3; }
	</style>
</head>
<body>
	<div class="container">
		<h1>NFC 出退勤</h1>
		<a class="btn btn-hi"  href="/slack?action=hi">出勤 (hi)</a>
		<a class="btn btn-bye" href="/slack?action=bye">退勤 (bye)</a>
	</div>
</body>
</html>`

func handleTop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, selectPage)
}

func handleSlackNotify(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	if action != "hi" && action != "bye" {
		http.Error(w, "Invalid action. Use ?action=hi or ?action=bye", http.StatusBadRequest)
		return
	}

	_, _, err := api.PostMessage(
		channelID,
		slack.MsgOptionText(action, false),
	)
	if err != nil {
		log.Printf("送信エラー: %s\n", err)
		http.Error(w, "Slackへの送信に失敗しました", http.StatusInternalServerError)
		return
	}

	label := "出勤"
	if action == "bye" {
		label = "退勤"
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>送信完了</title>
	<style>
		* { margin: 0; padding: 0; box-sizing: border-box; }
		body {
			display: flex; justify-content: center; align-items: center;
			height: 100vh; font-family: -apple-system, sans-serif;
			background: #f5f5f5;
		}
		.container { text-align: center; }
		h2 { font-size: 1.4rem; color: #333; margin-bottom: 0.5rem; }
		p  { color: #888; }
	</style>
</head>
<body>
	<div class="container">
		<h2>%s を送信しました</h2>
		<p>この画面は自動的に閉じます。</p>
	</div>
	<script>setTimeout(function(){ window.close(); }, 2000);</script>
</body>
</html>`, label)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func main() {
	_ = godotenv.Load()

	token := os.Getenv("SLACK_USER_TOKEN")
	channelID = os.Getenv("SLACK_CHANNEL_ID")

	if token == "" || channelID == "" {
		log.Fatal("SLACK_USER_TOKEN と SLACK_CHANNEL_ID を環境変数に設定してください")
	}

	api = slack.New(token)

	http.HandleFunc("/", handleTop)
	http.HandleFunc("/slack", handleSlackNotify)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("サーバーを起動しました: http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
