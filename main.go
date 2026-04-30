package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/joho/godotenv"

	"github.com/litousteven/news-summary-agent/pipeline"
)

func main() {
	slot := flag.String("slot", "manual", "推送档位: 00:00 / 12:00 / 18:00 / manual")
	configDir := flag.String("config", "./config", "配置目录路径")
	dataDir := flag.String("data", "./data", "数据目录路径 (运行时产出)")
	flag.Parse()

	_ = godotenv.Load()

	ctx := context.Background()

	// ChatModel — 标注和摘要共用
	chatModel, err := createChatModel(ctx)
	if err != nil {
		log.Fatalf("创建ChatModel失败: %v", err)
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

	p := &pipeline.NewsPipeline{
		ChatModel: chatModel,
		Embedding: embeddingClient,
		ConfigDir: absConfigDir,
		DataDir:   absDataDir,
		ProxyAddr: os.Getenv("PROXY_ADDR"),
		Config:    cfg,
	}

	// Cleanup expired data files before pipeline run
	p.CleanupExpiredFiles()

	result, err := p.Run(ctx, &pipeline.NewsSummaryRequest{
		Slot: *slot,
	})
	if err != nil {
		log.Fatalf("Pipeline执行失败: %v", err)
	}

	fmt.Println("========== 新闻简报 ==========")
	fmt.Println(result.Message)
	fmt.Printf("\n统计: 抓取=%d, 标注=%d, 入选=%d\n",
		result.Stats.TotalFetched, result.Stats.TotalTagged, result.Stats.TotalSelected)

	// Write push items to a timestamped .md file
	if len(result.DigestItems) > 0 {
		if err := writeDigestMD(absDataDir, result); err != nil {
			log.Printf("写入简报MD文件失败: %v", err)
		}
	}
}

// createChatModel creates a single OpenAI-compatible ChatModel.
func createChatModel(ctx context.Context) (model.BaseChatModel, error) {
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
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:  baseURL,
		Model:    modelName,
		APIKey:   apiKey,
		MaxTokens: &maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model: %w", err)
	}
	return chatModel, nil
}

// createEmbeddingClient creates an OpenAI-compatible embedding client.
// Returns nil if not configured (embedding-based dedup will be skipped).
func createEmbeddingClient() pipeline.EmbeddingClient {
	apiKey := os.Getenv("EMBEDDING_API_KEY")
	baseURL := os.Getenv("EMBEDDING_BASE_URL")
	modelName := os.Getenv("EMBEDDING_MODEL_NAME")

	if apiKey == "" || modelName == "" {
		log.Println("Embedding 未配置 (EMBEDDING_API_KEY/EMBEDDING_MODEL_NAME)，将跳过语义去重")
		return nil
	}

	return pipeline.NewOpenAIEmbeddingClient(baseURL, apiKey, modelName)
}

// writeDigestMD writes the pushed news items to a timestamped .md file.
func writeDigestMD(dataDir string, result *pipeline.NewsSummaryResult) error {
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
		prefix := ""
		if item.SeenBefore {
			prefix = "[追踪更新] "
		}
		b.WriteString(fmt.Sprintf("- %s%s\n", prefix, item.FactParagraph))
		if item.Link != "" {
			b.WriteString(fmt.Sprintf("  [%s](%s)\n", item.Source, item.Link))
		} else {
			b.WriteString(fmt.Sprintf("  来源: %s\n", item.Source))
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
