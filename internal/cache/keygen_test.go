package cache

import (
	"strings"
	"testing"

	"github.com/symbiotes/modelmesh/internal/provider"
)

func chatReq(content string) provider.ChatRequest {
	return provider.ChatRequest{Messages: []provider.ChatMessage{{Role: provider.RoleUser, Content: content}}}
}

func TestKeyGen_Deterministic(t *testing.T) {
	kg := NewKeyGenerator()
	a := kg.ChatKey("gpt-4o", chatReq("hello"))
	b := kg.ChatKey("gpt-4o", chatReq("hello"))
	if a != b {
		t.Errorf("same request produced different keys: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "chat:") {
		t.Errorf("chat key missing prefix: %q", a)
	}
}

func TestKeyGen_VariesByModel(t *testing.T) {
	kg := NewKeyGenerator()
	if kg.ChatKey("gpt-4o", chatReq("hi")) == kg.ChatKey("claude", chatReq("hi")) {
		t.Errorf("different models produced the same key")
	}
}

func TestKeyGen_VariesByContent(t *testing.T) {
	kg := NewKeyGenerator()
	if kg.ChatKey("m", chatReq("hi")) == kg.ChatKey("m", chatReq("bye")) {
		t.Errorf("different content produced the same key")
	}
}

func TestKeyGen_VariesBySamplingParams(t *testing.T) {
	kg := NewKeyGenerator()
	base := chatReq("hi")
	temp := 0.7
	withTemp := chatReq("hi")
	withTemp.Temperature = &temp
	if kg.ChatKey("m", base) == kg.ChatKey("m", withTemp) {
		t.Errorf("temperature change did not affect the key")
	}
}

func TestKeyGen_IgnoresMetadata(t *testing.T) {
	kg := NewKeyGenerator()
	a := chatReq("hi")
	b := chatReq("hi")
	b.Metadata = map[string]string{"trace": "abc"}
	if kg.ChatKey("m", a) != kg.ChatKey("m", b) {
		t.Errorf("non-semantic metadata affected the key")
	}
}

func TestKeyGen_ChatVsEmbeddingDisjoint(t *testing.T) {
	kg := NewKeyGenerator()
	ck := kg.ChatKey("m", chatReq("hi"))
	ek := kg.EmbeddingKey("m", provider.EmbeddingRequest{Input: []string{"hi"}})
	if ck == ek {
		t.Errorf("chat and embedding keys collided")
	}
	if !strings.HasPrefix(ek, "emb:") {
		t.Errorf("embedding key missing prefix: %q", ek)
	}
}

func TestKeyGen_EmbeddingInputOrderMatters(t *testing.T) {
	kg := NewKeyGenerator()
	a := kg.EmbeddingKey("m", provider.EmbeddingRequest{Input: []string{"a", "b"}})
	b := kg.EmbeddingKey("m", provider.EmbeddingRequest{Input: []string{"b", "a"}})
	if a == b {
		t.Errorf("embedding input order should affect the key (output order matters)")
	}
}
