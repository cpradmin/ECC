package handlers

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Tests for PromptLoader constructor and configuration

func TestNewPromptLoader(t *testing.T) {
	dataHome := t.TempDir()
	loader := NewPromptLoader(dataHome)

	if loader.dataHome != dataHome {
		t.Errorf("expected dataHome %s, got %s", dataHome, loader.dataHome)
	}

	expectedBaseDir := filepath.Join(dataHome, "ecc-prompts", "instincts")
	if loader.baseDir != expectedBaseDir {
		t.Errorf("expected baseDir %s, got %s", expectedBaseDir, loader.baseDir)
	}

	if loader.cache == nil {
		t.Fatal("expected cache to be initialized")
	}

	if loader.cache.ttl != DefaultCacheTTL {
		t.Errorf("expected TTL %v, got %v", DefaultCacheTTL, loader.cache.ttl)
	}
}

func TestNewPromptLoaderDefaultDataHome(t *testing.T) {
	// Save original HOME
	oldHome, ok := os.LookupEnv("HOME")
	if ok {
		defer os.Setenv("HOME", oldHome)
	}

	testHome := t.TempDir()
	os.Setenv("HOME", testHome)

	loader := NewPromptLoader("")

	expectedDataHome := filepath.Join(testHome, ".local/share")
	if loader.dataHome != expectedDataHome {
		t.Errorf("expected dataHome %s, got %s", expectedDataHome, loader.dataHome)
	}
}

func TestDefaultLoadTimeoutConstant(t *testing.T) {
	if DefaultLoadTimeout == 0 {
		t.Error("DefaultLoadTimeout should not be zero")
	}
	if DefaultLoadTimeout < 1*time.Second {
		t.Errorf("DefaultLoadTimeout should be at least 1s, got %v", DefaultLoadTimeout)
	}
}

func TestDefaultCacheTTLConstant(t *testing.T) {
	if DefaultCacheTTL == 0 {
		t.Error("DefaultCacheTTL should not be zero")
	}
	if DefaultCacheTTL < 1*time.Second {
		t.Errorf("DefaultCacheTTL should be at least 1s, got %v", DefaultCacheTTL)
	}
}

func TestSetIODelay(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())
	delay := 100 * time.Millisecond

	loader.setIODelay(delay)

	stored := loader.ioDelay.Load()
	if stored != int64(delay) {
		t.Errorf("expected delay %d, got %d", int64(delay), stored)
	}
}
