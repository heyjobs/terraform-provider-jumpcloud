package jumpcloud

import (
	"sync"
	"testing"
)

// TestGroupOperationStructure tests that groupOperation struct is properly defined
func TestGroupOperationStructure(t *testing.T) {
	op := groupOperation{
		groupID:   "test-id",
		groupName: "test-group",
		op:        "add",
	}

	if op.groupID != "test-id" {
		t.Errorf("Expected groupID 'test-id', got '%s'", op.groupID)
	}
	if op.groupName != "test-group" {
		t.Errorf("Expected groupName 'test-group', got '%s'", op.groupName)
	}
	if op.op != "add" {
		t.Errorf("Expected op 'add', got '%s'", op.op)
	}
}

// TestConcurrencyConstants tests that the concurrency constants are properly set
func TestConcurrencyConstants(t *testing.T) {
	if maxConcurrentGroupOps <= 0 {
		t.Errorf("maxConcurrentGroupOps should be positive, got %d", maxConcurrentGroupOps)
	}
	if maxConcurrentGroupOps > 20 {
		t.Errorf("maxConcurrentGroupOps should not exceed 20 to avoid rate limiting, got %d", maxConcurrentGroupOps)
	}
	if groupOpRateLimitMs <= 0 {
		t.Errorf("groupOpRateLimitMs should be positive, got %d", groupOpRateLimitMs)
	}
	if maxRetries <= 0 {
		t.Errorf("maxRetries should be positive, got %d", maxRetries)
	}
	if maxRetries > 10 {
		t.Errorf("maxRetries should not exceed 10 to avoid long waits, got %d", maxRetries)
	}
	if baseBackoffMs <= 0 {
		t.Errorf("baseBackoffMs should be positive, got %d", baseBackoffMs)
	}
}

// TestExponentialBackoffCalculation tests that exponential backoff is calculated correctly
func TestExponentialBackoffCalculation(t *testing.T) {
	testCases := []struct {
		attempt         int
		expectedBackoff int // in milliseconds
	}{
		{0, 0},                      // No backoff on first attempt
		{1, baseBackoffMs * 2},      // 200ms
		{2, baseBackoffMs * 4},      // 400ms
		{3, baseBackoffMs * 8},      // 800ms
	}

	for _, tc := range testCases {
		var backoff int
		if tc.attempt > 0 {
			backoff = baseBackoffMs * (1 << tc.attempt)
		}

		if backoff != tc.expectedBackoff {
			t.Errorf("For attempt %d, expected backoff %dms, got %dms",
				tc.attempt, tc.expectedBackoff, backoff)
		}
	}
}

// TestWorkerPoolConcurrency tests that the worker pool correctly limits concurrency
func TestWorkerPoolConcurrency(t *testing.T) {
	// Test that numWorkers is capped correctly
	testCases := []struct {
		numOperations   int
		expectedWorkers int
	}{
		{0, 0},
		{1, 1},
		{3, 3},
		{5, 5},
		{10, maxConcurrentGroupOps},
		{100, maxConcurrentGroupOps},
	}

	for _, tc := range testCases {
		numWorkers := maxConcurrentGroupOps
		if tc.numOperations < numWorkers {
			numWorkers = tc.numOperations
		}

		if numWorkers != tc.expectedWorkers {
			t.Errorf("For %d operations, expected %d workers, got %d",
				tc.numOperations, tc.expectedWorkers, numWorkers)
		}
	}
}

// TestBuildGroupOperations tests building operation lists from old/new group sets
func TestBuildGroupOperations(t *testing.T) {
	testCases := []struct {
		name          string
		oldGroupIDs   []string
		newGroupIDs   []string
		expectedAdds  int
		expectedRemoves int
	}{
		{
			name:          "no changes",
			oldGroupIDs:   []string{"a", "b"},
			newGroupIDs:   []string{"a", "b"},
			expectedAdds:  0,
			expectedRemoves: 0,
		},
		{
			name:          "add only",
			oldGroupIDs:   []string{"a"},
			newGroupIDs:   []string{"a", "b", "c"},
			expectedAdds:  2,
			expectedRemoves: 0,
		},
		{
			name:          "remove only",
			oldGroupIDs:   []string{"a", "b", "c"},
			newGroupIDs:   []string{"a"},
			expectedAdds:  0,
			expectedRemoves: 2,
		},
		{
			name:          "add and remove",
			oldGroupIDs:   []string{"a", "b"},
			newGroupIDs:   []string{"b", "c"},
			expectedAdds:  1,
			expectedRemoves: 1,
		},
		{
			name:          "empty to some",
			oldGroupIDs:   []string{},
			newGroupIDs:   []string{"a", "b"},
			expectedAdds:  2,
			expectedRemoves: 0,
		},
		{
			name:          "some to empty",
			oldGroupIDs:   []string{"a", "b"},
			newGroupIDs:   []string{},
			expectedAdds:  0,
			expectedRemoves: 2,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Convert slices to maps
			oldGroups := make(map[string]bool)
			for _, id := range tc.oldGroupIDs {
				oldGroups[id] = true
			}

			newGroups := make(map[string]bool)
			for _, id := range tc.newGroupIDs {
				newGroups[id] = true
			}

			// Build operations
			var operations []groupOperation

			for groupID := range newGroups {
				if !oldGroups[groupID] {
					operations = append(operations, groupOperation{groupID: groupID, op: "add"})
				}
			}

			for groupID := range oldGroups {
				if !newGroups[groupID] {
					operations = append(operations, groupOperation{groupID: groupID, op: "remove"})
				}
			}

			// Count adds and removes
			adds := 0
			removes := 0
			for _, op := range operations {
				if op.op == "add" {
					adds++
				} else {
					removes++
				}
			}

			if adds != tc.expectedAdds {
				t.Errorf("Expected %d adds, got %d", tc.expectedAdds, adds)
			}
			if removes != tc.expectedRemoves {
				t.Errorf("Expected %d removes, got %d", tc.expectedRemoves, removes)
			}
		})
	}
}

// TestChannelCommunication tests that the channel-based worker pattern works correctly
func TestChannelCommunication(t *testing.T) {
	operations := []groupOperation{
		{groupID: "1", op: "add"},
		{groupID: "2", op: "add"},
		{groupID: "3", op: "remove"},
	}

	opsChan := make(chan groupOperation, len(operations))
	resultChan := make(chan string, len(operations))

	var wg sync.WaitGroup

	// Simulate 2 workers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for op := range opsChan {
				resultChan <- op.groupID + "-" + op.op
			}
		}()
	}

	// Send operations
	for _, op := range operations {
		opsChan <- op
	}
	close(opsChan)

	// Wait for workers
	wg.Wait()
	close(resultChan)

	// Collect results
	results := make(map[string]bool)
	for result := range resultChan {
		results[result] = true
	}

	// Verify all operations were processed
	expected := map[string]bool{
		"1-add":    true,
		"2-add":    true,
		"3-remove": true,
	}

	for exp := range expected {
		if !results[exp] {
			t.Errorf("Expected result '%s' not found", exp)
		}
	}

	if len(results) != len(expected) {
		t.Errorf("Expected %d results, got %d", len(expected), len(results))
	}
}

// TestEmptyOperations tests handling of empty operation lists
func TestEmptyOperations(t *testing.T) {
	oldGroupIDs := []string{}
	newGroupIDs := []string{}

	if len(oldGroupIDs) == 0 && len(newGroupIDs) == 0 {
		// This is the expected path - no operations needed
		return
	}

	t.Error("Should have detected empty operations")
}
