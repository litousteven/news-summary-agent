package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/joho/godotenv"

	"github.com/litousteven/news-summary-agent/pipeline"
)

func main() {
	slot := flag.String("slot", "manual", "推送档位: 00:00 / 12:00 / 18:00 / manual")
	dataDir := flag.String("data", "./data", "数据目录路径")
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

	absDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		log.Fatalf("解析数据目录路径失败: %v", err)
	}
	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		log.Fatalf("创建数据目录失败: %v", err)
	}

	p := &pipeline.NewsPipeline{
		ChatModel: chatModel,
		Embedding: embeddingClient,
		DataDir:   absDataDir,
		ProxyAddr: os.Getenv("PROXY_ADDR"),
	}

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

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: baseURL,
		Model:   modelName,
		APIKey:  apiKey,
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
