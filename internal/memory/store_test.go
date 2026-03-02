package memory

import "testing"

func TestStorePersistsAndCompactsHistory(t *testing.T) {
	tmp := t.TempDir()
	store := NewStore(tmp, 2)
	chatID := int64(42)

	for i := 0; i < 3; i++ {
		if err := store.AppendExchange(chatID, "user message", "assistant message"); err != nil {
			t.Fatalf("AppendExchange() error = %v", err)
		}
	}

	state, err := store.Load(chatID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Summary == "" {
		t.Fatal("expected summary to be generated after compaction")
	}
	if got := len(state.Messages); got > 4 {
		t.Fatalf("expected compacted message count <= 4, got %d", got)
	}

	reloaded, err := store.Load(chatID)
	if err != nil {
		t.Fatalf("Load() after persist error = %v", err)
	}
	if reloaded.Summary == "" {
		t.Fatal("expected persisted summary in reloaded state")
	}
}
