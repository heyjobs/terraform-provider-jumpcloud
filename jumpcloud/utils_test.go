package jumpcloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
)

// TestGetUserGroupIDsUsesMemberOfEndpoint is a regression test for getUserGroupIDs
// calling GraphUserAssociationsList (a graph-of-direct-associations endpoint whose
// "targets" filter doesn't even accept "user_group" as a value) instead of
// GraphUserMemberOf (the endpoint that actually returns a user's group memberships).
// It fakes GET /users/{id}/memberof and asserts getUserGroupIDs extracts the group
// IDs from the returned GraphObjectWithPaths list.
func TestGetUserGroupIDsUsesMemberOfEndpoint(t *testing.T) {
	const userID = "user-1"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/"+userID+"/memberof" {
			t.Errorf("expected request to /users/%s/memberof, got %s", userID, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]jcapiv2.GraphObjectWithPaths{
			{Id: "group-a"},
			{Id: "group-b"},
		})
	}))
	defer server.Close()

	cfg := jcapiv2.NewConfiguration()
	cfg.BasePath = server.URL
	client := jcapiv2.NewAPIClient(cfg)

	groupIDs, err := getUserGroupIDs(client, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]bool{"group-a": true, "group-b": true}
	if len(groupIDs) != len(want) {
		t.Fatalf("expected %d group IDs, got %v", len(want), groupIDs)
	}
	for _, id := range groupIDs {
		if !want[id] {
			t.Errorf("unexpected group ID %q in result %v", id, groupIDs)
		}
	}
}
