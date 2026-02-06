# CLAUDE.md

このファイルは、このリポジトリでコードを扱う際のClaude Code (claude.ai/code) へのガイダンスを提供します。

## プロジェクト概要

gelfは、Vertex AI (Gemini) を使用してGitコミットメッセージの自動生成とAIによるPRタイトル/本文生成を提供するGo製CLIツールです。git変更を分析し、Bubble Teaで構築されたインタラクティブなTUIインターフェースを通じて適切なコミットメッセージ生成を提供します。

## アプリケーションアーキテクチャ

### 核となる機能

#### コミット機能
1. **ステージング変更をチェック** - `git diff --staged` でステージングされた変更を取得
2. **コミットメッセージ生成** - diffをVertex AI (Gemini) に送信してメッセージ生成
3. **ユーザー確認** - 生成されたメッセージをTUIでYes/No/編集プロンプトと共に表示
4. **コミット実行** - ユーザーが承認した場合に変更をコミット

#### PR作成機能
1. **差分取得** - ベースブランチとHEAD間のコミット/差分を取得
2. **AI生成** - Vertex AI (Gemini) に送信してPRタイトル/本文を生成
3. **確認・作成** - 生成結果を確認しPRを作成（またはdry-runで表示）

### プロジェクト構造
```
cmd/
├── root.go          # ルートコマンド定義
├── commit.go        # commitコマンド実装
└── pr.go            # prコマンド実装
internal/
  ├── git/
  │   └── diff.go      # Git操作 (staged/unstaged diff取得)
  ├── ai/
  │   └── vertex.go    # コミットメッセージ生成・PR生成のためのVertex AI統合
  ├── ui/
  │   └── tui.go       # Bubble Tea TUI実装（コミット）
  └── config/
      └── config.go    # 設定管理 (APIキーなど)
main.go               # アプリケーションエントリーポイント
```

### TUIフロー

#### コミットワークフロー
1. **起動** - ステージング変更の確認
2. **ローディング** - Vertex AI呼び出し中にスピナー表示
3. **確認** - 生成されたメッセージを[y/n/e]プロンプトと共に表示（編集機能付き）
4. **結果** - コミット成功/失敗を表示

### 技術仕様
- **コミット対象**: ステージング済み変更のみ (`git diff --staged`)
- **PR対象**: ベースブランチとHEAD間のコミット/差分
- **AIプロバイダー**: Vertex AI (Geminiモデル)
- **デフォルトFlashモデル**: gemini-3-flash-preview
- **デフォルトProモデル**: gemini-3-pro-preview
- **モデル設定**: 設定ファイル（gelf.yml）で変更可能
- **入力**: 生のgit diff出力 (フィルタリングなし)
- **UIフレームワーク**: Bubble Tea (TUI用)
- **マークダウン表示**: glamourライブラリによる整形表示（PR本文）
- **ユーザーインタラクション**: コミット承認・編集、PR作成確認

## コマンド使用法

```bash
# コミット関連
gelf commit                    # Vertex AIを使ってステージング済み変更をコミット（TUI付き）
gelf commit --dry-run          # コミットメッセージ生成のみ（diffも表示）
gelf commit --dry-run --quiet  # メッセージ生成のみ（外部ツール連携用）
gelf commit --model MODEL      # 一時的にモデルを変更

# PR関連
gelf pr create                 # PRタイトル/本文を生成して作成
gelf pr create --dry-run       # PRタイトル/本文の生成のみ表示

# ヘルプ
gelf --help          # ヘルプ表示
gelf commit --help   # コミットコマンドのヘルプ
gelf pr --help       # PRコマンドのヘルプ
```

## 開発コマンド

```bash
go mod init gelf     # Goモジュール初期化
go build                  # プロジェクトビルド
go test ./...             # テスト実行
go mod tidy               # 依存関係整理
go run main.go commit        # アプリケーション実行 (commitコマンド)
go run main.go commit --dry-run  # メッセージ生成のみのテスト
go run main.go pr create     # PR作成コマンド実行
```

## 依存関係

必要な主要Goモジュール：
- `github.com/charmbracelet/bubbletea` - TUIフレームワーク
- `github.com/charmbracelet/lipgloss` - スタイリングとレイアウト
- `github.com/charmbracelet/bubbles` - TUI components (spinner)
- `github.com/charmbracelet/glamour` - マークダウンレンダリング（PR本文表示用）
- `google.golang.org/genai` - Vertex AIクライアント
- `github.com/spf13/cobra` - CLIフレームワーク (サブコマンド実装用)
- `gopkg.in/yaml.v3` - YAML設定ファイルサポート

## 設定

アプリケーションには以下のVertex AI設定が必要です：
- Google Cloud プロジェクトID
- Vertex AI API認証情報
- モデル選択 (デフォルト: gemini-3-flash-preview)

### 設定ファイル

以下の場所で`gelf.yml`設定を管理できます（優先順）：

1. `./gelf.yml` - カレントディレクトリ（プロジェクト固有設定）
2. `$XDG_CONFIG_HOME/gelf/gelf.yml` - XDG設定ディレクトリ
3. `~/.config/gelf/gelf.yml` - XDG_CONFIG_HOMEが未設定の場合
4. `~/.gelf.yml` - ホームディレクトリ（従来形式）

```yaml
vertex_ai:
  project_id: "your-gcp-project-id"
  location: "global"

model:
  flash: "gemini-3-flash-preview"  # 高速処理用モデル
  pro: "gemini-3-pro-preview"       # 高品質処理用モデル
```

設定の優先順位（高い順）：
1. 環境変数
2. 設定ファイル
3. デフォルト値

## 環境変数

devcontainerの設定：
- `GOPROXY=direct`
- `GOPRIVATE=github.com/groove-x`

アプリケーションの環境変数：
- `GELF_CREDENTIALS` - サービスアカウントキーへのパス（GOOGLE_APPLICATION_CREDENTIALSより優先）
- `GOOGLE_APPLICATION_CREDENTIALS` - サービスアカウントキーへのパス
- `VERTEXAI_PROJECT` - Google CloudプロジェクトID
- `VERTEXAI_LOCATION` - Vertex AIのロケーション (デフォルト: global)

モデル設定は設定ファイル（gelf.yml）でのみ変更可能です。

## 覚書

- 仕様に変更があった場合は、README.md を更新すること

## 開発ガイドライン

- 変更後に、コンパイルしたときにエラーにならないように、staticcheck コマンドを使ってください。
- コードの修正後には、staticcheckを実行して、構文エラーがないか確認すること
