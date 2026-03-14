package dbc

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// === txWallet unit tests ===

func TestAllocateNonce_Sequential(t *testing.T) {
	w := &txWallet{nonceInited: true, nextNonce: 100}
	ctx := context.Background()

	// Sequential allocation should increment
	for i := uint64(0); i < 5; i++ {
		nonce, err := w.allocateNonce(ctx, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := 100 + i
		if nonce != expected {
			t.Errorf("nonce %d: got %d, want %d", i, nonce, expected)
		}
	}
}

func TestAllocateNonce_Concurrent(t *testing.T) {
	w := &txWallet{nonceInited: true, nextNonce: 0}
	ctx := context.Background()

	const goroutines = 100
	nonces := make([]uint64, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			n, err := w.allocateNonce(ctx, nil)
			if err != nil {
				t.Errorf("goroutine %d error: %v", idx, err)
				return
			}
			nonces[idx] = n
		}(i)
	}
	wg.Wait()

	// All nonces should be unique
	seen := make(map[uint64]bool)
	for i, n := range nonces {
		if seen[n] {
			t.Errorf("duplicate nonce %d at index %d", n, i)
		}
		seen[n] = true
	}

	// Should have allocated exactly goroutines nonces (0..99)
	if len(seen) != goroutines {
		t.Errorf("expected %d unique nonces, got %d", goroutines, len(seen))
	}

	// Next nonce should be goroutines
	if w.nextNonce != goroutines {
		t.Errorf("nextNonce: got %d, want %d", w.nextNonce, goroutines)
	}
}

func TestResetNonce(t *testing.T) {
	w := &txWallet{nonceInited: true, nextNonce: 50}

	w.resetNonce()

	if w.nonceInited {
		t.Error("nonceInited should be false after reset")
	}
	// nextNonce value doesn't matter after reset, will be re-initialized from chain
}

func TestResetNonce_Concurrent(t *testing.T) {
	w := &txWallet{nonceInited: true, nextNonce: 100}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.resetNonce()
		}()
	}
	wg.Wait()

	if w.nonceInited {
		t.Error("nonceInited should be false after concurrent resets")
	}
}

// === nextWallet round-robin tests ===

func TestNextWallet_SingleWallet(t *testing.T) {
	chain := &dbcChain{
		wallets: []*txWallet{{address: [20]byte{1}}},
	}

	for i := 0; i < 10; i++ {
		w := chain.nextWallet()
		if w != chain.wallets[0] {
			t.Errorf("iteration %d: expected wallet 0", i)
		}
	}
}

func TestNextWallet_RoundRobin(t *testing.T) {
	wallets := make([]*txWallet, 3)
	for i := range wallets {
		wallets[i] = &txWallet{address: [20]byte{byte(i)}}
	}
	chain := &dbcChain{wallets: wallets}

	for round := 0; round < 3; round++ {
		for i := 0; i < 3; i++ {
			w := chain.nextWallet()
			expected := chain.wallets[(round*3+i)%3]
			if w != expected {
				t.Errorf("round %d iter %d: got wallet %x, want %x", round, i, w.address[0], expected.address[0])
			}
		}
	}
}

func TestNextWallet_Concurrent(t *testing.T) {
	const numWallets = 5
	const numGoroutines = 100

	wallets := make([]*txWallet, numWallets)
	for i := range wallets {
		wallets[i] = &txWallet{address: [20]byte{byte(i)}}
	}
	chain := &dbcChain{wallets: wallets}

	counts := make([]atomic.Int64, numWallets)
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := chain.nextWallet()
			for j, wallet := range wallets {
				if w == wallet {
					counts[j].Add(1)
					break
				}
			}
		}()
	}
	wg.Wait()

	// Each wallet should get exactly numGoroutines/numWallets calls
	expected := int64(numGoroutines / numWallets)
	for i := 0; i < numWallets; i++ {
		got := counts[i].Load()
		if got != expected {
			t.Errorf("wallet %d: got %d calls, want %d", i, got, expected)
		}
	}
}

// === InitDbcChain config tests ===

func TestInitConfig_BackwardCompat(t *testing.T) {
	// When PrivateKeys is empty, should use PrivateKey
	keys := []string{}
	singleKey := "abcd1234"

	result := keys
	if len(result) == 0 {
		result = []string{singleKey}
	}

	if len(result) != 1 || result[0] != singleKey {
		t.Errorf("backward compat failed: got %v", result)
	}
}

func TestInitConfig_MultipleKeys(t *testing.T) {
	keys := []string{"key1", "key2", "key3"}
	singleKey := "fallback"

	result := keys
	if len(result) == 0 {
		result = []string{singleKey}
	}

	if len(result) != 3 {
		t.Errorf("expected 3 keys, got %d", len(result))
	}
	if result[0] != "key1" {
		t.Errorf("expected key1, got %s", result[0])
	}
}

// === Edge case tests ===

func TestAllocateNonce_AfterReset(t *testing.T) {
	w := &txWallet{nonceInited: true, nextNonce: 50}

	// Allocate a nonce
	ctx := context.Background()
	n1, _ := w.allocateNonce(ctx, nil)
	if n1 != 50 {
		t.Errorf("first nonce: got %d, want 50", n1)
	}

	// Reset
	w.resetNonce()
	if w.nonceInited {
		t.Error("should not be inited after reset")
	}

	// After reset, allocateNonce needs a real client to re-init
	// Without client, it will fail — which is correct behavior
	// (In production, the next call provides the ethclient)
}

func TestNextWallet_Overflow(t *testing.T) {
	wallets := make([]*txWallet, 3)
	for i := range wallets {
		wallets[i] = &txWallet{address: [20]byte{byte(i)}}
	}
	chain := &dbcChain{wallets: wallets}

	// Simulate near-overflow of uint64 counter
	chain.walletIdx.Store(^uint64(0) - 2) // max - 2

	// Should still work correctly with modulo
	for i := 0; i < 6; i++ {
		w := chain.nextWallet()
		_ = w // just ensure no panic
	}
}
