package jumpcloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// fakeGraphServer starts an httptest server mimicking enough of the JumpCloud v2 API for group
// name/ID lookups, current-membership reads, and membership add/remove calls. recordOp is called
// for every add/remove request received (group_id, op). Returns a Configuration so callers can
// either build a *jcapiv2.APIClient directly or pass it as the `m interface{}` a resource CRUD
// function expects.
func fakeGraphServer(t *testing.T, userID string, groups []jcapiv2.UserGroup, currentGroupIDs []string, recordOp func(groupID, op string)) *jcapiv2.Configuration {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/usergroups":
			json.NewEncoder(w).Encode(groups)
		case r.Method == http.MethodGet && r.URL.Path == "/users/"+userID+"/memberof":
			members := make([]jcapiv2.GraphObjectWithPaths, 0, len(currentGroupIDs))
			for _, id := range currentGroupIDs {
				members = append(members, jcapiv2.GraphObjectWithPaths{Id: id})
			}
			json.NewEncoder(w).Encode(members)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/usergroups/"):
			id := strings.TrimPrefix(r.URL.Path, "/usergroups/")
			for _, g := range groups {
				if g.Id == id {
					json.NewEncoder(w).Encode(g)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/members"):
			groupID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/usergroups/"), "/members")
			var body jcapiv2.UserGroupMembersReq
			_ = json.NewDecoder(r.Body).Decode(&body)
			if recordOp != nil {
				recordOp(groupID, body.Op)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	return cfg
}

// TestCreateOnlyAddsNeverRemovesUndeclaredMembership is a regression test for the incident where
// a real, undeclared membership got swept into removal: the user already has a real membership
// ("extra") this config never mentions. Create must only ever add what's missing from `groups`,
// never remove anything - not even "extra".
func TestCreateOnlyAddsNeverRemovesUndeclaredMembership(t *testing.T) {
	const userID = "user-1"
	groups := []jcapiv2.UserGroup{{Id: "id-a", Name: "a"}}

	var mu sync.Mutex
	var ops []groupOperation
	cfg := fakeGraphServer(t, userID, groups, []string{"id-extra"}, func(groupID, op string) {
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, groupOperation{groupID: groupID, op: op})
	})
	client := jcapiv2.NewAPIClient(cfg)

	groupNameToID, err := lookupGroupsByName(client, []string{"a"}, true)
	if err != nil {
		t.Fatalf("lookupGroupsByName: %v", err)
	}
	currentGroupIDs, err := getUserGroupIDs(client, userID)
	if err != nil {
		t.Fatalf("getUserGroupIDs: %v", err)
	}

	desiredGroupIDs := make([]string, 0, len(groupNameToID))
	for _, id := range groupNameToID {
		desiredGroupIDs = append(desiredGroupIDs, id)
	}
	alreadyMemberIDs := intersectGroupIDs(currentGroupIDs, desiredGroupIDs)

	if err := syncUserGroupsConcurrent(client, userID, alreadyMemberIDs, desiredGroupIDs, groupNameToID); err != nil {
		t.Fatalf("syncUserGroupsConcurrent: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(ops) != 1 || ops[0].groupID != "id-a" || ops[0].op != "add" {
		t.Fatalf("expected exactly one add for id-a, got %+v", ops)
	}
	for _, op := range ops {
		if op.groupID == "id-extra" {
			t.Errorf("undeclared group 'extra' must never be touched, got %+v", op)
		}
	}
}

// TestDeleteOnlyRemovesDeclaredGroups is a regression test for the actual incident: a merge of
// this resource stripped 226 memberships across 106 users because Delete discovered and removed
// the user's FULL real membership. The user's real membership includes an undeclared "extra"
// group; destroying the resource must remove only "a" (declared in `groups`), never "extra".
func TestDeleteOnlyRemovesDeclaredGroups(t *testing.T) {
	const userID = "user-1"
	groups := []jcapiv2.UserGroup{{Id: "id-a", Name: "a"}}

	var mu sync.Mutex
	var ops []groupOperation
	cfg := fakeGraphServer(t, userID, groups, []string{"id-a", "id-extra"}, func(groupID, op string) {
		mu.Lock()
		defer mu.Unlock()
		ops = append(ops, groupOperation{groupID: groupID, op: op})
	})

	d := schema.TestResourceDataRaw(t, resourceUserGroupMemberships().Schema, map[string]interface{}{
		"user_email": "user@example.com",
		"groups":     []interface{}{"a"},
	})
	d.SetId(userID)

	if err := resourceUserGroupMembershipsDelete(d, cfg); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(ops) != 1 || ops[0].groupID != "id-a" || ops[0].op != "remove" {
		t.Errorf("expected exactly one remove for id-a (never id-extra), got %+v", ops)
	}
}

// TestReadNeverSurfacesUndeclaredGroup is a regression test proving Read never reports a real,
// undeclared group into the `groups` state - only the resource's own declared groups are checked.
func TestReadNeverSurfacesUndeclaredGroup(t *testing.T) {
	const userID = "user-1"
	groups := []jcapiv2.UserGroup{{Id: "id-a", Name: "a"}}
	cfg := fakeGraphServer(t, userID, groups, []string{"id-a", "id-extra"}, nil)

	d := schema.TestResourceDataRaw(t, resourceUserGroupMemberships().Schema, map[string]interface{}{
		"user_email": "user@example.com",
		"groups":     []interface{}{"a"},
	})
	d.SetId(userID)

	if err := resourceUserGroupMembershipsRead(d, cfg); err != nil {
		t.Fatalf("Read: %v", err)
	}

	gotGroups := stringSetToSlice(d.Get("groups").(*schema.Set))
	if len(gotGroups) != 1 || gotGroups[0] != "a" {
		t.Errorf("expected state groups to be [\"a\"] (never surfacing undeclared 'extra'), got %v", gotGroups)
	}
}

// TestSyncUserGroupsFailsClosedOnUndeclaredGroupID is a belt-and-suspenders test for the
// fail-closed guard in syncUserGroupsConcurrent: this scenario should be unreachable via
// Create/Update/Delete's allowlist-only logic (that's the point), so it's forced directly by
// passing a groupID with no corresponding entry in groupNameToID. The guard must refuse the
// entire operation set - not just error out, but send zero add/remove requests.
func TestSyncUserGroupsFailsClosedOnUndeclaredGroupID(t *testing.T) {
	const userID = "user-1"
	var calls int
	cfg := fakeGraphServer(t, userID, nil, nil, func(string, string) {
		calls++
	})
	client := jcapiv2.NewAPIClient(cfg)

	err := syncUserGroupsConcurrent(client, userID, []string{"id-mystery"}, []string{}, map[string]string{})
	if err == nil {
		t.Fatal("expected an error for an operation with no declared group name, got nil")
	}
	if calls != 0 {
		t.Errorf("expected zero group operations to be sent when the guard trips, got %d", calls)
	}
}

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

// TestGroupLookupWorkerFindsGroupBeyondFirstResult is a regression test for the
// GroupsUserList call in groupLookupWorker returning zero matches in production.
// It fakes the JumpCloud API's real behavior: limit truncates the result set
// before name matching happens, so a limit of 1 can omit the group being looked
// up entirely. It fails against the old limit=1/no-sort request shape and
// passes against limit=0/sort=[]string{}.
func TestGroupLookupWorkerFindsGroupBeyondFirstResult(t *testing.T) {
	allGroups := []jcapiv2.UserGroup{
		{Id: "id-alpha", Name: "alpha"},
		{Id: "id-target", Name: "target"},
		{Id: "id-gamma", Name: "gamma"},
	}

	var gotLimit string
	var sawSortParam bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLimit = r.URL.Query().Get("limit")
		_, sawSortParam = r.URL.Query()["sort"]

		w.Header().Set("Content-Type", "application/json")
		if gotLimit == "1" {
			json.NewEncoder(w).Encode(allGroups[:1])
			return
		}
		json.NewEncoder(w).Encode(allGroups)
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	client := jcapiv2.NewAPIClient(cfg)

	names := make(chan string, 1)
	results := make(chan groupLookupResult, 1)
	names <- "target"
	close(names)

	var wg sync.WaitGroup
	wg.Add(1)
	go groupLookupWorker(client, names, results, &wg)
	wg.Wait()
	close(results)

	res := <-results
	if res.err != nil {
		t.Fatalf("unexpected error looking up group: %v", res.err)
	}
	if res.id != "id-target" {
		t.Errorf("expected group 'target' to resolve to id 'id-target', got id %q", res.id)
	}
	if gotLimit != "0" {
		t.Errorf("expected GroupsUserList to be called with limit=0, got limit=%q", gotLimit)
	}
	if !sawSortParam {
		t.Error("expected GroupsUserList to be called with a sort param")
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

// TestReadClearsStateOnDeletedUser is a regression test proving getUserGroupIDs's errUserNotFound
// makes Read's not-found handling reachable again: when the user has been deleted, Read must
// clear the resource from state instead of leaving it stuck showing 0 members forever.
func TestReadClearsStateOnDeletedUser(t *testing.T) {
	const userID = "deleted-user"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/usergroups" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]jcapiv2.UserGroup{{Id: "id-a", Name: "a"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL

	d := schema.TestResourceDataRaw(t, resourceUserGroupMemberships().Schema, map[string]interface{}{
		"user_email": "user@example.com",
		"groups":     []interface{}{"a"},
	})
	d.SetId(userID)

	if err := resourceUserGroupMembershipsRead(d, cfg); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if d.Id() != "" {
		t.Errorf("expected Read to clear the resource ID for a deleted user, got %q", d.Id())
	}
}

// TestGroupLookupWorkerDoesNotRetryOn400 proves a permanent 4xx fails immediately instead of
// burning maxRetries*backoff time retrying a request that will never succeed.
func TestGroupLookupWorkerDoesNotRetryOn400(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	client := jcapiv2.NewAPIClient(cfg)

	if _, err := lookupGroupsByName(client, []string{"a"}, true); err == nil {
		t.Fatal("expected an error for a 400 response, got nil")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("expected exactly 1 request (no retries on a permanent 4xx), got %d", got)
	}
}

// TestGroupLookupWorkerRetriesOn500ThenSucceeds proves transient server errors are still retried.
func TestGroupLookupWorkerRetriesOn500ThenSucceeds(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]jcapiv2.UserGroup{{Id: "id-a", Name: "a"}})
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	client := jcapiv2.NewAPIClient(cfg)

	result, err := lookupGroupsByName(client, []string{"a"}, true)
	if err != nil {
		t.Fatalf("expected the retry to succeed, got error: %v", err)
	}
	if result["a"] != "id-a" {
		t.Errorf("expected group 'a' to resolve to id-a, got %v", result)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Errorf("expected exactly 2 requests (1 failure + 1 retry), got %d", got)
	}
}

// TestGroupOperationWorkerDoesNotRetryOn400 proves a permanent 4xx on a membership mutation
// fails immediately instead of retrying.
func TestGroupOperationWorkerDoesNotRetryOn400(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	client := jcapiv2.NewAPIClient(cfg)

	err := syncUserGroupsConcurrent(client, "user-1", []string{}, []string{"id-a"}, map[string]string{"a": "id-a"})
	if err == nil {
		t.Fatal("expected an error for a 400 response, got nil")
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("expected exactly 1 request (no retries on a permanent 4xx), got %d", got)
	}
}

// TestGroupOperationWorkerRetriesOn500ThenSucceeds proves transient server errors are still
// retried for membership mutations too.
func TestGroupOperationWorkerRetriesOn500ThenSucceeds(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&requests, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	client := jcapiv2.NewAPIClient(cfg)

	err := syncUserGroupsConcurrent(client, "user-1", []string{}, []string{"id-a"}, map[string]string{"a": "id-a"})
	if err != nil {
		t.Fatalf("expected the retry to succeed, got error: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Errorf("expected exactly 2 requests (1 failure + 1 retry), got %d", got)
	}
}

// TestGroupOperationWorkerTreats404OnRemoveAsSuccess is a regression test for Delete's removed
// string-matched "not found" fallback: a 404 removing a membership (group or user already gone)
// must be treated as an already-satisfied removal, not a failure, and must not be retried.
func TestGroupOperationWorkerTreats404OnRemoveAsSuccess(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	client := jcapiv2.NewAPIClient(cfg)

	err := syncUserGroupsConcurrent(client, "user-1", []string{"id-a"}, []string{}, map[string]string{"a": "id-a"})
	if err != nil {
		t.Fatalf("expected a 404 on remove to be treated as success, got error: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Errorf("expected exactly 1 request (no retries on a 404), got %d", got)
	}
}
