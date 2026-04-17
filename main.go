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

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>送信完了</title>
</head>
<body style="display:flex;justify-content:center;align-items:center;height:100vh;font-family:sans-serif;">
	<div style="text-align:center;">
		<h2>Slackに "%s" を送信しました</h2>
		<p>この画面は自動的に閉じます。</p>
	</div>
	<script>setTimeout(function(){ window.close(); }, 2000);</script>
</body>
</html>`, action)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func main() {
	// ローカル開発時は .env から読み込み、クラウドでは環境変数を直接使う
	_ = godotenv.Load()

	token := os.Getenv("SLACK_USER_TOKEN")
	channelID = os.Getenv("SLACK_CHANNEL_ID")

	if token == "" || channelID == "" {
		log.Fatal("SLACK_USER_TOKEN と SLACK_CHANNEL_ID を環境変数に設定してください")
	}

	api = slack.New(token)

	http.HandleFunc("/slack", handleSlackNotify)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("サーバーを起動しました: http://localhost:%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
