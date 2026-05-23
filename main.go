package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/joho/godotenv"

	"github.com/litousteven/news-summary-agent/pipeline"
	"github.com/litousteven/news-summary-agent/pipeline/embedding"
	"github.com/litousteven/news-summary-agent/pipeline/types"
)

func main() {
	slot := flag.String("slot", "manual", "推送档位: 00:00 / 12:00 / 18:00 / manual")
	configDir := flag.String("config", "./config", "配置目录路径")
	dataDir := flag.String("data", "./data", "数据目录路径 (运行时产出)")
	flag.Parse()

	_ = godotenv.Load()

	// Setup log: both stderr and daily-rotated file under log/
	setupLogging()

	ctx := context.Background()

	// ChatModel — 标注和摘要共用（非 JSON 模式）
	chatModel, err := createChatModel(ctx, false)
	if err != nil {
		log.Fatalf("创建ChatModel失败: %v", err)
	}

	// TagChatModel — 标注专用（JSON 强制模式）
	tagChatModel, err := createChatModel(ctx, true)
	if err != nil {
		log.Printf("创建TagChatModel失败: %v，将使用通用ChatModel继续", err)
		tagChatModel = nil
	}

	// Embedding — OpenAI兼容的 Embedding 模型
	embeddingClient := createEmbeddingClient()

	absConfigDir, err := filepath.Abs(*configDir)
	if err != nil {
		log.Fatalf("解析配置目录路径失败: %v", err)
	}

	absDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatalf("解析数据目录路径失败: %v", err)
	}
	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		log.Fatalf("创建数据目录失败: %v", err)
	}

	// Load config from config/config.yaml
	cfg := pipeline.LoadConfig(absConfigDir)

	// Initialize embedding cache
	embedCache := embedding.NewEmbeddingCache(absDataDir)
	embedCache.Load()

	p := &pipeline.NewsPipeline{
		ChatModel:    chatModel,
		TagChatModel: tagChatModel,
		Embedding:    embeddingClient,
		EmbedCache:   embedCache,
		ConfigDir:    absConfigDir,
		DataDir:      absDataDir,
		ProxyAddr:    os.Getenv("PROXY_ADDR"),
		Config:       cfg,
	}

	// Cleanup expired data files before pipeline run
	p.CleanupExpiredFiles()

	result, err := p.Run(ctx, &types.NewsSummaryRequest{
		Slot: *slot,
	})
	if err != nil {
		log.Fatalf("Pipeline执行失败: %v", err)
	}

	fmt.Println("========== 新闻简报 ==========")
	fmt.Println(result.Message)
	fmt.Printf("\n统计: 抓取=%d, 标注=%d, 入选=%d\n",
		result.Stats.TotalFetched, result.Stats.TotalTagged, result.Stats.TotalSelected)
	if result.Stats.TaggingFailed > 0 {
		pct := float64(result.Stats.TaggingFailed) / float64(result.Stats.TotalFetched) * 100
		fmt.Printf("标记: 标注失败 %d 条 (%.0f%%), 流程已忽略并继续\n",
			result.Stats.TaggingFailed, pct)
	}

	// Save embedding cache
	embedCache.Save()

	// Write push items to a timestamped .md file
	if len(result.DigestItems) > 0 {
		if err := writeDigestMD(absDataDir, result); err != nil {
			log.Printf("写入简报MD文件失败: %v", err)
		}
	}
}

// createChatModel creates a single OpenAI-compatible ChatModel.
// jsonMode=true forces response_format to json_object.
func createChatModel(ctx context.Context, jsonMode bool) (model.BaseChatModel, error) {
	apiKey := os.Getenv("CHAT_MODEL_API_KEY")
	baseURL := os.Getenv("CHAT_MODEL_BASE_URL")
	modelName := os.Getenv("CHAT_MODEL_NAME")

	if apiKey == "" {
		return nil, fmt.Errorf("missing CHAT_MODEL_API_KEY")
	}
	if modelName == "" {
		return nil, fmt.Errorf("missing CHAT_MODEL_NAME")
	}

	maxTokens := 16384
	cfg := &openai.ChatModelConfig{
		BaseURL:   baseURL,
		Model:     modelName,
		APIKey:    apiKey,
		MaxTokens: &maxTokens,
	}
	if jsonMode {
		cfg.ResponseFormat = &openai.ChatCompletionResponseFormat{
			Type: "json_object",
		}
	}
	chatModel, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create chat model: %w", err)
	}
	return chatModel, nil
}

// createEmbeddingClient creates an OpenAI-compatible embedding client.
// Returns nil if not configured (embedding-based dedup will be skipped).
func createEmbeddingClient() *embedding.OpenAIEmbeddingClient {
	apiKey := os.Getenv("EMBEDDING_API_KEY")
	baseURL := os.Getenv("EMBEDDING_BASE_URL")
	modelName := os.Getenv("EMBEDDING_MODEL_NAME")

	if apiKey == "" || modelName == "" {
		log.Println("Embedding 未配置 (EMBEDDING_API_KEY/EMBEDDING_MODEL_NAME)，将跳过语义去重")
		return nil
	}

	return embedding.NewOpenAIEmbeddingClient(baseURL, apiKey, modelName)
}

// writeDigestMD writes the pushed news items to a timestamped .md file.
func writeDigestMD(dataDir string, result *types.NewsSummaryResult) error {
	now := time.Now()
	filename := fmt.Sprintf("digest_%s.md", now.Format("20060102_150405"))
	path := filepath.Join(dataDir, filename)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("# 新闻简报 %s\n\n", now.Format("2006-01-02 15:04")))

	currentCat := ""
	for _, item := range result.DigestItems {
		if item.Category != currentCat {
			if currentCat != "" {
				b.WriteString("\n")
			}
			currentCat = item.Category
			b.WriteString(fmt.Sprintf("## %s\n\n", currentCat))
		}
		b.WriteString(fmt.Sprintf("- %s\n", item.FactParagraph))
		if item.Link != "" {
			b.WriteString(fmt.Sprintf("  [%s](%s)\n", item.Source, item.Link))
		} else {
			b.WriteString(fmt.Sprintf("  来源: %s\n", item.Source))
		}
		// Render references as Markdown quotes
		for _, ref := range item.Refs {
			if ref.Link != "" {
				b.WriteString(fmt.Sprintf("  > %s: [%s](%s)（%s）\n", ref.RelationNote, ref.DisplayTitle, ref.Link, ref.Source))
			} else {
				b.WriteString(fmt.Sprintf("  > %s: %s（%s）\n", ref.RelationNote, ref.DisplayTitle, ref.Source))
			}
		}
	}

	b.WriteString(fmt.Sprintf("\n---\n统计: 抓取=%d, 标注=%d, 入选=%d\n",
		result.Stats.TotalFetched, result.Stats.TotalTagged, result.Stats.TotalSelected))

	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write md file: %w", err)
	}
	log.Printf("简报已写入: %s", path)
	return nil
}

// setupLogging configures log output to both stderr and a daily-rotated file under log/.
func setupLogging() {
	absLogDir, err := filepath.Abs("./log")
	if err != nil {
		log.Printf("解析log目录路径失败: %v, 日志仅输出到stderr", err)
		return
	}
	if err := os.MkdirAll(absLogDir, 0755); err != nil {
		log.Printf("创建log目录失败: %v, 日志仅输出到stderr", err)
		return
	}

	dateStr := time.Now().Format("20060102")
	logPath := filepath.Join(absLogDir, "pipeline_"+dateStr+".log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("打开日志文件失败: %v, 日志仅输出到stderr", err)
		return
	}

	multiWriter := io.MultiWriter(os.Stderr, f)
	log.SetOutput(multiWriter)
	log.SetFlags(log.Ldate | log.Ltime)
	log.Printf("日志文件: %s", logPath)
}
