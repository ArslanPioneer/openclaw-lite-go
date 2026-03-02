package runtime

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"openclaw-lite-go/internal/config"
	"openclaw-lite-go/internal/telegram"
)

type benchBot struct{}

func (b *benchBot) GetUpdates(_ context.Context, _ int64, _ int) ([]telegram.Update, error) {
	return nil, nil
}

func (b *benchBot) SendMessage(_ context.Context, _ int64, _ string) error {
	return nil
}

type benchAgent struct{}

func (a *benchAgent) GenerateReply(_ context.Context, _ string, _ string) (string, error) {
	return "ok", nil
}

type benchToolLoopAgent struct{}

func (a *benchToolLoopAgent) GenerateReply(_ context.Context, prompt string, _ string) (string, error) {
	if strings.Contains(prompt, "Step 1 tool call: echo") {
		return "final answer", nil
	}
	return `TOOL_CALL {"name":"echo","text":"bench-tool-output"}`, nil
}

func BenchmarkHandleUpdateSerial(b *testing.B) {
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, &benchBot{}, &benchAgent{})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		update := telegram.Update{
			UpdateID: int64(i + 1),
			Message: &telegram.Message{
				Chat: telegram.Chat{ID: 42},
				Text: "hello",
			},
		}
		if err := svc.HandleUpdate(ctx, update); err != nil {
			b.Fatalf("HandleUpdate() error = %v", err)
		}
	}
}

func BenchmarkHandleUpdateParallel(b *testing.B) {
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, &benchBot{}, &benchAgent{})
	ctx := context.Background()
	var updateID int64

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			next := atomic.AddInt64(&updateID, 1)
			update := telegram.Update{
				UpdateID: next,
				Message: &telegram.Message{
					Chat: telegram.Chat{ID: next % 64},
					Text: "hello",
				},
			}
			if err := svc.HandleUpdate(ctx, update); err != nil {
				b.Fatalf("HandleUpdate() error = %v", err)
			}
		}
	})
}

func BenchmarkHandleUpdateToolLoop(b *testing.B) {
	svc := NewService(config.Config{
		Agent: config.AgentConfig{Model: "gpt-4o-mini"},
	}, &benchBot{}, &benchToolLoopAgent{})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		update := telegram.Update{
			UpdateID: int64(i + 1),
			Message: &telegram.Message{
				Chat: telegram.Chat{ID: 99},
				Text: "run tool loop",
			},
		}
		if err := svc.HandleUpdate(ctx, update); err != nil {
			b.Fatalf("HandleUpdate() error = %v", err)
		}
	}
}
