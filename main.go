package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go"
)

func main() {
	// chats フォルダを作成(存在しない場合は作成する)
	err := os.MkdirAll("chats", 0755)
	if err != nil {
		fmt.Println("Error creating chats directory:", err)
		return
	}

	// -----------------------------
	// 1) chats/ 配下のファイルと「新規チャット」の選択画面を出す
	// -----------------------------
	chatFile, err := selectChatFile()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	// -----------------------------
	// 2) 選択されたファイルをパース or 新規ファイルを作成
	// -----------------------------
	messages := []openai.ChatCompletionMessageParamUnion{}
	var f *os.File

	if chatFile == "" {
		// chatFile が空文字の場合は「新規チャット」が選ばれたと判断
		newFilename := createNewChatFileName() // 新しいファイル名を生成する
		filePath := filepath.Join("chats", newFilename)
		// ファイルを新規作成し、追記モードで開く
		f, err = os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Println("Error creating new chat file:", err)
			return
		}
		// messages は空のまま(新規チャット)
		fmt.Printf("新しいチャットファイル '%s' を作成しました\n", newFilename)
	} else {
		// 既存ファイルが選ばれた場合
		filePath := filepath.Join("chats", chatFile)

		// まず読み込み用に開いて parse
		existingMessages, err := parseChatHistory(filePath)
		if err != nil {
			fmt.Println("Error parsing chat history:", err)
			return
		}
		messages = append(messages, existingMessages...)

		// 読み込み用ファイルを閉じた後、追記モードで開き直す
		f, err = os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Println("Error opening chat file:", err)
			return
		}
		fmt.Printf("既存ファイル '%s' を読み込みました\n", chatFile)
	}
	defer f.Close()

	// -----------------------------
	// 3) 通常どおり ChatGPT CLI を開始
	// -----------------------------
	client := openai.NewClient()
	ctx := context.Background()

	fmt.Println("-----------------------------------")
	fmt.Println("ChatGPT CLI を開始します。")
	fmt.Println("会話を送信するには、半角スペースのみの行を入力して改行してください。")
	fmt.Println("終了するには \"exit\" と入力し、さらに空行を入力してください。")
	fmt.Println("-----------------------------------")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		userInput := readMultilineInput(scanner)
		if userInput == "" {
			// 何も入力されずに空行だけの場合は無視
			continue
		}
		if userInput == "exit" {
			fmt.Println("チャットを終了します。")
			break
		}

		// ユーザメッセージを履歴に追加
		messages = append(messages, openai.UserMessage(userInput))

		// Markdown に追記
		writeMarkdown(f, "User", userInput)

		completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Messages: openai.F(messages),
			Model:    openai.F(openai.ChatModelO1),
		})
		if err != nil {
			fmt.Println("Error:", err)
			continue
		}

		// AIからの応答
		assistantMessage := completion.Choices[0].Message.Content
		fmt.Println(assistantMessage)
		messages = append(messages, openai.AssistantMessage(assistantMessage))

		// Markdown に追記
		writeMarkdown(f, "Assistant", assistantMessage)
	}
}

// readMultilineInput は、複数行を読み取り、空行（半角スペースのみの行）が入力されたらまとめて返す関数
func readMultilineInput(scanner *bufio.Scanner) string {
	var lines []string
	fmt.Println("---------")
	fmt.Println("入力を開始（半角スペースのみの行で送信）：")
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			// Ctrl+D やファイル終端などで終了した場合
			break
		}
		line := scanner.Text()
		if line == " " {
			// 半角スペースのみの行が入力されたら入力終了
			break
		}
		lines = append(lines, line)
	}
	fmt.Println("---------")
	return strings.Join(lines, "\n")
}

// writeMarkdown は、役割(role)とメッセージ(message)を Markdown 形式でファイルに追記する関数
func writeMarkdown(f *os.File, role, message string) {
	// 役割ごとに、見出しを付ける。
	// Userは「## User」、Assistantは「## Assistant」
	f.WriteString(fmt.Sprintf("## %s\n\n%s\n\n", role, message))
}

// -----------------------------
// Markdownをパースして messages に変換する関数
// -----------------------------
func parseChatHistory(filename string) ([]openai.ChatCompletionMessageParamUnion, error) {
	// ファイルを開く
	f, err := os.Open(filename)
	if err != nil {
		// ファイルが存在しないなど
		if os.IsNotExist(err) {
			// 無視して空スライスを返す
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var messages []openai.ChatCompletionMessageParamUnion
	var currentRole string
	var currentLines []string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		// 「## 」で始まる行は"役割"とみなす
		if strings.HasPrefix(line, "## ") {
			// もし前の役割とメッセージがたまっていたら messages に追加
			if currentRole != "" && len(currentLines) > 0 {
				msg := strings.Join(currentLines, "\n")
				messages = append(messages, convertToOpenAIPayload(currentRole, msg))
			}
			// 新しい役割をセット
			currentRole = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			// メッセージバッファを初期化
			currentLines = []string{}
		} else {
			// 役割以外の行はメッセージの本文としてためる
			// （空行でも改行扱いでつなげたいならここで処理）
			currentLines = append(currentLines, line)
		}
	}
	// ループ終了後に残っていたら最後に追加
	if currentRole != "" && len(currentLines) > 0 {
		msg := strings.Join(currentLines, "\n")
		messages = append(messages, convertToOpenAIPayload(currentRole, msg))
	}

	// scanner のエラーチェック
	if err := scanner.Err(); err != nil {
		return messages, err
	}

	return messages, nil
}

// 役割文字列を openai.ChatCompletionMessageParamUnion に変換する小関数
func convertToOpenAIPayload(role, content string) openai.ChatCompletionMessageParamUnion {
	switch role {
	case "Assistant":
		return openai.AssistantMessage(content)
	case "User":
		return openai.UserMessage(content)
	default:
		// デフォルトは User 扱いにする
		return openai.UserMessage(content)
	}
}

// ------------------------------
// ここから下は選択・ファイル名生成用のユーティリティ
// ------------------------------

// selectChatFile は、chats/ フォルダ内のファイルを一覧表示し、
// その中から1つ選ばせるか「新規チャット」を選ばせる。
//
// 選ばれたファイル名（"xxx.md"）を返す。新規なら空文字("")を返す。
func selectChatFile() (string, error) {
	files, err := listMarkdownFiles("chats")
	if err != nil {
		return "", err
	}

	// 表示用に "新規チャット" を末尾に追加
	// files には Markdownファイル名の一覧が格納されている
	fmt.Println("▼チャットを選択してください:")
	fmt.Printf("[%d] 新規チャット\n", 0)
	for i, f := range files {
		fmt.Printf("[%d] %s\n", i+1, f)
	}

	// 入力受付
	var s string
	for {
		fmt.Print("選択番号を入力してください > ")
		fmt.Scanln(&s)
		idx, err := strconv.Atoi(s)
		if err != nil {
			fmt.Println("数値を入力してください。")
			continue
		}
		if idx < 0 || idx > len(files) {
			fmt.Println("選択肢の番号を入力してください。")
			continue
		}

		// 選択肢の最後が「新規チャット」
		if idx == 0 {
			// 新規チャット
			return "", nil
		}

		// 既存ファイルが選ばれた
		selectedFile := files[idx-1]
		return selectedFile, nil
	}
}

// listMarkdownFiles は指定ディレクトリ配下の .md ファイルを一覧として返す
func listMarkdownFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) == ".md" {
			files = append(files, e.Name())
		}
	}
	return files, nil
}

// createNewChatFileName は、新規チャット用のユニークなファイル名を生成する関数(例: chat_20241225_123456.md)
func createNewChatFileName() string {
	// 例: 日付や時刻、UUIDなどを入れる
	// ここでは簡単に YYYYMMDD_HHMMSS 形式を例示
	// 実際には "github.com/google/uuid" など使ってUUIDを生成してもよい
	// あるいはユーザーにファイル名を入力させてもよい
	return fmt.Sprintf("chat_%s.md", nowString())
}

// nowString は"YYYYMMDD_HHMMSS" 形式の文字列を返す
func nowString() string {
	// 現在時刻を"YYYYMMDD_HHMMSS" 形式の文字列に変換
	return time.Now().Format("20060102_150405")
}
